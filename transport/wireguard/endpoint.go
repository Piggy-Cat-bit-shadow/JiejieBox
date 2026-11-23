package wireguard

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	N "github.com/sagernet/sing/common/network"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing-box/service/powerreport"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
	"github.com/sagernet/wireguard-go/conn"
	"github.com/sagernet/wireguard-go/device"
	wgTun "github.com/sagernet/wireguard-go/tun"

	"go4.org/netipx"
)

type Endpoint struct {
	options        EndpointOptions
	peers          []peerConfig
	ipcConf        string
	allowedAddress []netip.Prefix
	// staticAllowedIPs answers Lookup/PreferredAddress from the configuration alone.
	//
	// It exists because the address a peer owns must keep the same answer while the heavy chain is
	// released: the Device's AllowedIPs trie is emptied by Device.Close (RemoveAllPeers), so reading
	// it after a suspend would turn every configured address into "not mine" and change routing.
	// The trie is built in the same peer/prefixed order IpcSet uses, with one zero-value Peer per
	// configured peer as the key. The returned *device.Peer is only ever compared with nil by the
	// product (protocol/wireguard PreferredAddress); its identity is not part of the answer.
	staticAllowedIPs *device.AllowedIPs
	tunDevice        Device
	device           atomic.Pointer[device.Device]
	returnPath       atomic.Pointer[returnPathState]
	egressPool       *tun.UDPEgressPool
	pause            pause.Manager
	pauseCallback    *list.Element[pause.Callback]
	// deviceWakeCallback is the DeviceWake half of the recovery nudge. It is separate from
	// pauseCallback because the two events mean different things; see onDeviceWake.
	deviceWakeCallback *list.Element[pause.Callback]
	stateAccess        sync.Mutex
	suspended          atomic.Bool
	networkPaused      bool
	// closing is set by Close under stateAccess. Start checks it in the same critical section, so
	// a Close that lands while the device is still being built cannot leave a live device behind
	// a closed tun, and cannot resurrect a nil device pointer after teardown.
	closing bool
	// ---- heavy-chain lifecycle (W5 selective lazy) ----
	//
	// lazyEligible is the shape verdict computed at construction: private userspace, no listening
	// port, no keepalive timer, every peer a configured IP endpoint. Only this shape may release the
	// wireguard-go Device on suspend; everything else keeps the exact previous Down-only behaviour.
	lazyEligible bool
	// stackStarted records that tunDevice.Start has run. The gVisor stack is started exactly once;
	// a rebuild creates a new Device over the same stack, it does not restart the stack.
	stackStarted bool
	// everPublished records that a heavy chain has been published at least once, so a missing
	// device means "released by idle" rather than "never started". A never-started endpoint keeps
	// its old error surface (the stack is not running, so a dial fails from the stack itself).
	everPublished bool
	// addressingReady gates Lookup: before the first publish the previous implementation had no
	// address table at all, and a query answered nil.
	addressingReady bool
	// deferred records that the heavy chain was released by suspend and must be rebuilt on demand.
	deferred bool
	// udpAssociations counts the UDP associations that are still open through this endpoint.
	//
	// It is an OWNERSHIP boundary for the release, not a statistic. A released chain keeps the stack
	// and its flows but destroys the Device; TCP survives that because the transport retransmits, so
	// the demand path can rebuild the chain and the flow continues. UDP has no retransmission: one
	// datagram IS the request, and the independent review measured a single post-wake request timing
	// out even though the Device rebuilt. Releasing under a live UDP association therefore turns a
	// wake into data loss, and the association is exactly the thing that still needs the Device.
	udpAssociations atomic.Int32
	// activeUses counts calls that are currently creating a flow on the stack. It is the mutual
	// exclusion between "a dial is in flight" and "release the device": suspend only releases when
	// this is zero, otherwise it keeps the previous Down-only behaviour for that pause.
	activeUses int
	// buildAccess serialises builds against releases. A build and a release can both touch the bind's
	// egress provider (the bind's Close calls SetEgressPort(0)), so they must never overlap. Lock
	// order is always buildAccess -> stateAccess.
	buildAccess sync.Mutex
	// buildDeviceHook is the failure-injection seam for rebuild tests. Production never sets it.
	buildDeviceHook func() error
	// recovery is the runtime-lifecycle state: which peers are stale, and the worker that acts on
	// it. See recovery.go and docs/fork/runtime-lifecycle-phase1.5.md.
	recovery recoveryState
	// registration is this endpoint's handle on the runtime coordinator, used to coalesce and
	// bound rebinds. It is inert (not nil) when no coordinator is installed.
	registration       *runtimecoord.Registration
	removeRegistration func()
}

func NewEndpoint(options EndpointOptions) (*Endpoint, error) {
	if options.PrivateKey == "" {
		return nil, E.New("missing private key")
	}
	privateKeyBytes, err := base64.StdEncoding.DecodeString(options.PrivateKey)
	if err != nil {
		return nil, E.Cause(err, "decode private key")
	}
	privateKey := hex.EncodeToString(privateKeyBytes)
	ipcConf := "private_key=" + privateKey
	if options.ListenPort != 0 {
		ipcConf += "\nlisten_port=" + F.ToString(options.ListenPort)
	}
	// AmneziaWG device-global obfuscation lines (jc=/h1=/i1=...). awgIpcLines is tag-gated:
	// with with_awg it formats the parameters, and without it a non-empty AWG configuration is
	// an explicit error instead of being silently sent as plain WireGuard.
	awgLines, err := awgIpcLines(options.AmneziaWG)
	if err != nil {
		return nil, err
	}
	ipcConf += awgLines
	var peers []peerConfig
	for peerIndex, rawPeer := range options.Peers {
		peer := peerConfig{
			allowedIPs: rawPeer.AllowedIPs,
			keepalive:  rawPeer.PersistentKeepaliveInterval,
		}
		if rawPeer.Endpoint.Addr.IsValid() {
			peer.endpoint = rawPeer.Endpoint.AddrPort()
		} else if rawPeer.Endpoint.IsDomain() {
			peer.destination = rawPeer.Endpoint
		}
		publicKeyBytes, err := base64.StdEncoding.DecodeString(rawPeer.PublicKey)
		if err != nil {
			return nil, E.Cause(err, "decode public key for peer ", peerIndex)
		}
		if len(publicKeyBytes) != device.NoisePublicKeySize {
			return nil, E.New("invalid public key for peer ", peerIndex, ", required ", device.NoisePublicKeySize, " bytes, got ", len(publicKeyBytes))
		}
		peer.publicKey = device.NoisePublicKey(publicKeyBytes)
		if rawPeer.PreSharedKey != "" {
			preSharedKeyBytes, err := base64.StdEncoding.DecodeString(rawPeer.PreSharedKey)
			if err != nil {
				return nil, E.Cause(err, "decode pre shared key for peer ", peerIndex)
			}
			peer.preSharedKeyHex = hex.EncodeToString(preSharedKeyBytes)
		}
		if len(rawPeer.AllowedIPs) == 0 {
			return nil, E.New("missing allowed ips for peer ", peerIndex)
		}
		if len(rawPeer.Reserved) > 0 {
			if len(rawPeer.Reserved) != 3 {
				return nil, E.New("invalid reserved value for peer ", peerIndex, ", required 3 bytes, got ", len(rawPeer.Reserved))
			}
			copy(peer.reserved[:], rawPeer.Reserved[:])
		}
		peers = append(peers, peer)
	}
	var allowedPrefixBuilder netipx.IPSetBuilder
	for _, peer := range options.Peers {
		for _, prefix := range peer.AllowedIPs {
			allowedPrefixBuilder.AddPrefix(prefix)
		}
	}
	allowedIPSet, err := allowedPrefixBuilder.IPSet()
	if err != nil {
		return nil, err
	}
	allowedAddresses := allowedIPSet.Prefixes()
	if options.MTU == 0 {
		options.MTU = 1408
	}
	if err = validateTunnelMTU(options.MTU, options.Address, options.MTUBoundedBy, options.MTUBoundedRequired); err != nil {
		return nil, err
	}
	// The address table the product queries must not depend on a live Device (see the field comment).
	staticAllowedIPs := &device.AllowedIPs{}
	staticPeers := make([]*device.Peer, len(peers))
	for peerIndex := range staticPeers {
		staticPeers[peerIndex] = &device.Peer{}
	}
	for peerIndex, peer := range peers {
		for _, allowedIP := range peer.allowedIPs {
			staticAllowedIPs.Insert(allowedIP, staticPeers[peerIndex])
		}
	}
	// Shape gating for the selective lazy release. Every condition is a reason the old code kept
	// the Device resident for a purpose the release cannot preserve:
	//   - a listen port is an address passive peers and monitoring rely on;
	//   - keepalive is a timer whose phase a rebuild resets;
	//   - a system stack configures host routes and ports;
	//   - a peer without a configured endpoint is passive (inbound demand), and a domain peer starts
	//     at post-start so a rebuild would re-resolve it at a different time.
	lazyEligible := options.ListenPort == 0 && !options.System && len(peers) > 0
	for _, peer := range peers {
		if !peer.endpoint.IsValid() || peer.keepalive != 0 {
			lazyEligible = false
		}
	}
	return &Endpoint{
		options:          options,
		peers:            peers,
		ipcConf:          ipcConf,
		allowedAddress:   allowedAddresses,
		staticAllowedIPs: staticAllowedIPs,
		lazyEligible:     lazyEligible,
	}, nil
}

func (e *Endpoint) Initialize(memoryPressure func() tun.MemoryPressure) error {
	e.initRecovery()
	// Registered for the endpoint's lifetime: the registration is what bounds recovery to one
	// rebind per window and makes a network change cancel a rebind that belonged to the old one.
	// The coordinator is optional, so an endpoint built without a core still works.
	if coordinator := service.FromContext[*runtimecoord.Coordinator](e.options.Context); coordinator != nil {
		e.registration, e.removeRegistration = coordinator.Register(e.RuntimeResourceLabel())
	} else {
		e.registration, e.removeRegistration = (*runtimecoord.Coordinator)(nil).Register(e.RuntimeResourceLabel())
	}
	options := e.options
	deviceOptions := DeviceOptions{
		Context:         options.Context,
		Logger:          options.Logger,
		System:          options.System,
		Handler:         options.Handler,
		UDPTimeout:      options.UDPTimeout,
		ICMPTimeout:     options.ICMPTimeout,
		UDPMapping:      options.UDPMapping,
		UDPFiltering:    options.UDPFiltering,
		UDPNATMax:       options.UDPNATMax,
		InterfaceFinder: options.InterfaceFinder,
		MemoryPressure:  memoryPressure,
		CreateDialer:    options.CreateDialer,
		Name:            options.Name,
		MTU:             options.MTU,
		Address:         options.Address,
		AllowedAddress:  e.allowedAddress,
	}
	tunDevice, err := NewDevice(deviceOptions)
	if err != nil {
		return E.Cause(err, "create WireGuard device")
	}
	e.tunDevice = tunDevice
	if stack, isStack := tunDevice.(*stackDevice); isStack {
		// The demand half of the release protocol: a retained flow's packet is what asks for the
		// rebuild, because only a new Dial used to be able to. A system stack keeps its own Device
		// lifecycle and never takes the release path, so it is not wired.
		stack.setWake(e.rebuildOnTrafficDemand)
	}
	return nil
}

func (e *Endpoint) Start(postStart bool) error {
	hasDomainPeer := common.Any(e.peers, func(peer peerConfig) bool {
		return peer.destination.IsDomain()
	})
	if postStart != hasDomainPeer {
		return nil
	}
	// Box.Close is legal at any point of Box.Start - the daemon deliberately lets a stop arrive
	// while a start is still running. The expensive part of the build runs OUTSIDE stateAccess so
	// a concurrent Close is not held up by it, and the publish step at the end re-checks the
	// closing flag under the lock: a start that lost the race closes what it built instead of
	// publishing a live device on top of a torn-down endpoint.
	//
	// Holding the lock across the whole build was the first shape of this fix, and it is wrong:
	// wgDevice.IpcSet brings the device up, which runs the pause/network callbacks, and those
	// take the same lock - a self-deadlock.
	// An endpoint with no dialer has no way to reach the network at all, and the bind it would build
	// from it (ClientBind, since a nil dialer cannot provide the listener capability) would fail
	// later, from wireguard-go's own goroutines. Refusing here reports it as the configuration error
	// it is, before a tun stack and a device have been built for a tunnel that cannot carry a packet.
	if e.options.Dialer == nil {
		return E.New("missing dialer for wireguard endpoint")
	}
	// Builds and releases are serialised: they share the bind's egress provider, and a release must
	// not be able to close a bind that a build is still installing. Lock order is buildAccess then
	// stateAccess everywhere.
	e.buildAccess.Lock()
	defer e.buildAccess.Unlock()
	bind, err := e.newBind()
	if err != nil {
		return err
	}
	if err = e.startStack(); err != nil {
		return err
	}
	wgDevice, wgPeers, err := e.buildDevice(bind)
	if err != nil {
		return err
	}
	// Register the pause callbacks BEFORE taking the lock.
	//
	// # Why this call may not sit inside the critical section (LX 084's family)
	//
	// The pause manager invokes every callback INLINE, while holding its own `access` mutex
	// (sing/service/pause/default.go: `DevicePause` takes `d.access` and calls `d.emit`, a plain
	// `for … element.Value(event)` loop with no unlock in between). `onPauseUpdated` takes
	// `stateAccess`, because it must read `e.device` and publish `e.networkPaused`. Registering
	// under `stateAccess` therefore put the two locks in both orders at once:
	//
	//	startDevice / Close : stateAccess -> pause.access   (RegisterCallback / UnregisterCallback)
	//	DevicePause et al.  : pause.access -> stateAccess   (emit -> onPauseUpdated)
	//
	// That is an ABBA deadlock, and its blast radius is the LX 084 one rather than a stalled
	// endpoint: `pause.access` is held forever, so every later `emit`, `RegisterCallback` and
	// `RegisterTicker` blocks behind it - including `URLTestGroup.Touch`'s `RegisterTicker` on the
	// DIAL path, which takes it under the group's own lock. Every new flow through every urltest
	// group stops, with no log line, until the process restarts.
	//
	// `Endpoint.Close` runs on every config reload and every Box teardown, and the pause events
	// arrive from platform power handlers on an independent goroutine set, so the pair is reached by
	// ordinary use rather than by an exotic interleaving.
	//
	// # What replaces the lock here, and why it is enough
	//
	// The lock was there so a `Close` that is already waiting sees the callbacks and gives them up.
	// That is preserved by ordering instead of by mutual exclusion: the handles are PUBLISHED under
	// `stateAccess` below, in the same critical section that checks `closing`. So the two orders are
	// exhaustive -
	//
	//   - Close first: `closing` is set, the check below returns os.ErrClosed before anything is
	//     published, and the registration is undone on the way out;
	//   - registration first: the handles are already in `e.pauseCallback` when Close takes the
	//     lock, and Close gives them up.
	//
	// The callback is registered before the device is published, which is the safe direction: an
	// event that arrives in that window finds `e.device` nil and returns, exactly as it did before
	// this change, and the publish below is what makes later events effective.
	//
	// # Why the delivery stays SYNCHRONOUS
	//
	// A pause is a resource contract, not a notification: `EventNetworkPause` must have RELEASED the
	// UDP socket by the time `DevicePause()` returns, which is what the platform suspend path relies
	// on (`TestPauseWakeCyclesInterleavedWithRebindsKeepOneSocket` asserts the release at the
	// boundary with no grace period). Handing the event to a queue would make that contract a race,
	// so the cycle is broken on THIS side instead of by deferring the callback.
	var pauseManager pause.Manager
	var registeredPause, registeredWake *list.Element[pause.Callback]
	pauseManager = service.FromContext[pause.Manager](e.options.Context)
	registered := pauseManager != nil
	if registered {
		// REGISTERED HERE, outside stateAccess, which is the whole point of the lock-order note
		// above: the pre-fix code registered these while holding stateAccess, so a manager that was
		// mid-`emit` (holding pause.access and waiting for stateAccess) deadlocked against a Start
		// that held stateAccess and wanted pause.access. Doing it before the lock is taken removes
		// that cycle while keeping the callbacks SYNCHRONOUS, which the resource contract needs.
		//
		// These two assignments were MISSING. `34cc5834d9a` moved the registration out of the locked
		// section as this comment describes and deleted the calls without re-adding them, so
		// registeredPause/registeredWake were always nil and the publish below stored nils: the
		// endpoint never received EventNetworkPause, and the DeviceWake recovery nudge never fired.
		// Nothing caught it because the package's tests register the callbacks BY HAND on a fake
		// manager and never call Start, so no test exercised the production registration path.
		registeredPause = pauseManager.RegisterCallback(e.onPauseUpdated)
		registeredWake = pauseManager.RegisterCallback(e.onDeviceWake)
	}

	e.stateAccess.Lock()
	if e.closing {
		// Close ran while this device was being built. Nothing was published, so nothing owns the
		// device: close it here rather than leaving it running against a torn-down endpoint.
		// The registrations above are released OUTSIDE the lock, so a Close that is already holding
		// it is not blocked behind us.
		e.stateAccess.Unlock()
		if registered {
			pauseManager.UnregisterCallback(registeredPause)
			pauseManager.UnregisterCallback(registeredWake)
		}
		wgDevice.Close()
		return os.ErrClosed
	}
	e.publishLocked(wgDevice, wgPeers)
	e.pause = pauseManager
	if registered {
		// Published under the lock, in the same critical section as the `closing` check above: that
		// pairing is what replaces the mutual exclusion the registration used to hold.
		e.pauseCallback = registeredPause
		e.deviceWakeCallback = registeredWake
	}
	// A pause that arrived before this device was published could not be applied by the callback
	// (it saw a nil device), and bindListenPort has already brought the device up. Apply the current
	// state now, under the same lock the callback takes, so a pause-before-first-packet start does
	// not leave a live socket on a paused network.
	if pauseManager != nil && pauseManager.IsNetworkPaused() {
		e.networkPaused = true
		wgDevice.Down()
	}
	e.stateAccess.Unlock()

	// Observe session transitions. Set AFTER the initial IpcSet, so the configuration's own
	// handshake start is already accounted for, and before any traffic can drive a retry.
	//
	// The callback contract is strict (see sessionStateChanged): cheap, serialized per peer, and it
	// must not call back into Device. It records state and nudges the recovery worker; nothing else.
	wgDevice.SetSessionStateFunc(e.sessionStateChanged)
	return nil
}

// startStack starts the gVisor stack exactly once. A rebuild creates a new wireguard-go Device
// over the same stack; it must not restart the stack, because that would tear down every flow the
// suspension contract promised to preserve.
func (e *Endpoint) startStack() error {
	e.stateAccess.Lock()
	started := e.stackStarted
	e.stateAccess.Unlock()
	if started {
		return nil
	}
	if err := e.tunDevice.Start(); err != nil {
		return err
	}
	e.stateAccess.Lock()
	e.stackStarted = true
	e.stateAccess.Unlock()
	return nil
}

// newBind builds one bind. It is called once per heavy chain: at Start and again on every rebuild,
// because closing the Device closes the bind with it.
//
// The egress pool is NOT rebuilt: it is owned by the endpoint and only attached to the bind. The
// bind's Close only calls SetEgressPort(0) on it, so a release/reopen pair does not churn the
// interface registration. The pool's three mandatory callbacks are still validated here, before a
// device exists, exactly as they were when this code lived in Start.
func (e *Endpoint) newBind() (conn.Bind, error) {
	var bind conn.Bind
	udpListener, isUDPListener := common.Cast[dialer.UDPListener](e.options.Dialer)
	if isUDPListener {
		listenerControl, egressEnabled := udpListener.UDPListenerControl()
		// Only the bind gets the family-tolerant view. The egress pool keeps the raw control,
		// where a per-member failure is already tolerated by the pool itself.
		standardBind := conn.NewStdNetBind(familyTolerantListenerControl(listenerControl)).(*conn.StdNetBind)
		if e.options.ListenPort == 0 && len(e.peers) == 1 && e.peers[0].endpoint.IsValid() {
			standardBind.SetSinglePeerMode()
		}
		if egressEnabled {
			// The egress pool dereferences three fields of this struct UNCONDITIONALLY, from
			// wireguard-go's own goroutines: InterfaceMonitor at SetEgressPort (sing-tun
			// udp_egress.go:119), InterfaceFinder at its bind and at Close (:125, :177, :83), and Logger
			// on the warning path (:218). Their ZERO VALUE is therefore not "unset", it is a nil
			// dereference inside RoutineTUNEventReader - reached from BindUpdate, which is reached from
			// the recovery worker - where no recover can catch it and the process dies.
			//
			// MEASURED: a caller that sets only the fields it cares about panics the whole test binary
			// the first time the bind opens a port.
			//
			// The in-tree callers are all safe - protocol/wireguard/endpoint.go, protocol/openvpn and
			// protocol/tailscale each fill all three from their network manager - so this refuses
			// exactly the configuration that would have crashed later, and refuses it as the
			// configuration error it is, BEFORE a device exists. A check here rather than in sing-tun
			// because the missing guard is in the pinned fork, which this repository does not write to.
			egressPoolOptions := e.options.EgressPoolOptions
			if egressPoolOptions.InterfaceMonitor == nil {
				return nil, E.New("missing InterfaceMonitor in EgressPoolOptions for wireguard endpoint ",
					e.options.Tag, ": the UDP egress pool dereferences it when it takes a port (sing-tun ",
					"udp_egress.go:119) and the failure would be a nil pointer dereference on ",
					"wireguard-go's own goroutine rather than an error here")
			}
			if egressPoolOptions.InterfaceFinder == nil {
				return nil, E.New("missing InterfaceFinder in EgressPoolOptions for wireguard endpoint ",
					e.options.Tag, ": the UDP egress pool registers an interface callback with it ",
					"(sing-tun udp_egress.go:125) and the failure would be a nil pointer dereference on ",
					"wireguard-go's own goroutine rather than an error here")
			}
			if egressPoolOptions.Logger == nil {
				return nil, E.New("missing Logger in EgressPoolOptions for wireguard endpoint ",
					e.options.Tag, ": the UDP egress pool logs a per-member listen failure through it ",
					"(sing-tun udp_egress.go:218), and a failure that has nothing to report it with is a ",
					"nil pointer dereference instead of the warning it was meant to be")
			}
			if e.egressPool == nil {
				egressPoolOptions.Control = listenerControl
				e.egressPool = tun.NewUDPEgressPool(egressPoolOptions)
			}
			standardBind.SetEgressProvider(e.egressPool)
		}
		powerManager := service.FromContext[*powerreport.Manager](e.options.Context)
		if powerManager != nil {
			recorder := powerManager.Recorder()
			if recorder != nil {
				attribution := &powerreport.Attribution{Endpoint: e.options.Tag}
				counter := recorder.TrafficCounter(powerreport.TrafficEndpoint, e.options.Tag)
				standardBind.SetIOActivityFuncs(func(size int) {
					counter.CountIn(int64(size))
					recorder.Touch(powerreport.DirectionInbound, size, attribution)
				}, func(size int) {
					counter.CountOut(int64(size))
					recorder.Touch(powerreport.DirectionOutbound, size, attribution)
				})
			}
		}
		bind = standardBind
	} else {
		var (
			isConnect   bool
			connectAddr netip.AddrPort
			reserved    [3]uint8
		)
		if len(e.peers) == 1 {
			reserved = e.peers[0].reserved
			if e.peers[0].endpoint.IsValid() {
				isConnect = true
				connectAddr = e.peers[0].endpoint
			}
		}
		clientBind := NewClientBind(e.options.Context, e.options.Logger, e.options.Dialer, isConnect, connectAddr, reserved)
		if e.options.AmneziaWG.IsSet() {
			// AWG protects the bytes after the message type; the Reserved-byte rewrite this bind
			// performs for WARP-style peers would corrupt every protected header.
			clientBind.SetAWGTransparent(true)
		}
		bind = clientBind
	}
	if isUDPListener || len(e.peers) > 1 {
		for _, peer := range e.peers {
			if peer.endpoint.IsValid() && peer.reserved != [3]uint8{} {
				bind.SetReservedForEndpoint(peer.endpoint, peer.reserved)
			}
		}
	}
	return bind, nil
}

// newTunTarget gives one wireguard-go Device its private tun view.
//
// The gVisor-backed stack hands out a fresh attachment per Device, which is what makes "close the
// Device without closing the stack" possible. Devices that are not attachment-capable (the system
// stack and the package's test doubles) keep the historical shape: one tun object reused as the
// wireguard-go tun. Those shapes are not eligible for the lazy release anyway.
func (e *Endpoint) newTunTarget() wgTun.Device {
	if provider, isProvider := e.tunDevice.(attachmentProvider); isProvider {
		return provider.newAttachment()
	}
	return e.tunDevice
}

// buildDevice constructs one complete heavy chain: the tun target, the wireguard-go Device, the IPC
// configuration and the up transition. The caller owns publication and the failure path (the
// returned error already closed what this function built).
func (e *Endpoint) buildDevice(bind conn.Bind) (*device.Device, []*device.Peer, error) {
	logger := &device.Logger{
		Verbosef: func(format string, args ...any) {
			e.options.Logger.Debug(fmt.Sprintf(strings.ToLower(format), args...))
		},
		Errorf: func(format string, args ...any) {
			e.options.Logger.Error(fmt.Sprintf(strings.ToLower(format), args...))
		},
	}
	wgDevice := device.NewDevice(e.options.Context, &returnDeviceWrapper{Device: e.newTunTarget(), endpoint: e}, bind, logger, e.options.Workers)
	domainPeers := make(map[device.NoisePublicKey]*peerConfig)
	for peerIndex, peer := range e.peers {
		if peer.destination.IsDomain() {
			domainPeers[peer.publicKey] = &e.peers[peerIndex]
		}
	}
	if len(domainPeers) > 0 {
		wgDevice.SetEndpointResolverFunc(func(publicKey device.NoisePublicKey) ([]conn.Endpoint, error) {
			peer, found := domainPeers[publicKey]
			if !found {
				return nil, nil
			}
			addresses, lookupErr := e.options.ResolvePeer(peer.destination.Fqdn)
			if lookupErr != nil {
				return nil, lookupErr
			}
			endpoints := make([]conn.Endpoint, 0, len(addresses))
			for _, address := range addresses {
				destination := netip.AddrPortFrom(address, peer.destination.Port)
				if peer.reserved != ([3]uint8{}) {
					bind.SetReservedForEndpoint(destination, peer.reserved)
				}
				endpoint, parseErr := bind.ParseEndpoint(destination.String())
				if parseErr != nil {
					return nil, parseErr
				}
				endpoints = append(endpoints, endpoint)
			}
			return endpoints, nil
		})
	}
	var ipcConf strings.Builder
	ipcConf.WriteString(e.ipcConf)
	for _, peer := range e.peers {
		ipcConf.WriteString(peer.GenerateIpcLines())
	}
	err := wgDevice.IpcSet(ipcConf.String())
	if err != nil {
		wgDevice.Close()
		// The IPC configuration is NOT included, and that is the point.
		//
		// # What was leaking
		//
		// This returned `E.Cause(err, "setup wireguard: \n", ipcConf.String())`, and that string
		// begins `private_key=<hex>` and carries every peer's `preshared_key=<hex>`. A failed IpcSet
		// is a ROUTINE event - a malformed allowed_ip, a key the kernel rejects, a port conflict -
		// and each one put the device's identity key and every PSK into an error that the caller
		// logs, wraps, or hands to an SDK. An operator pasting that error into a bug report, or a
		// crash reporter shipping it, would disclose the keys themselves.
		//
		// # Why the fix is not a redaction pass
		//
		// Filtering the text would mean deciding, at the point of the error, which substrings are
		// secret - and that decision is exactly the kind that goes stale: a new IPC field, a new key
		// format, a peer option that carries a token. The configuration is therefore not passed at
		// all. What is kept is everything an operator needs to locate the failure: the subsystem,
		// the underlying parser error (which names the offending FIELD, not its value), and the peer
		// count, so a multi-peer configuration can be narrowed down.
		//
		// The underlying error is NOT swallowed: it is the cause, so `errors.Is`/`errors.As` and the
		// full text still work.
		return nil, nil, E.Cause(err, "setup wireguard: configure device with ",
			strconv.Itoa(len(e.peers)), " peer(s); the configuration itself is deliberately omitted from "+
				"this error because it contains private key material")
	}
	if err = e.bindListenPort(wgDevice); err != nil {
		wgDevice.Close()
		return nil, nil, err
	}
	wgPeers := make([]*device.Peer, 0, len(e.peers))
	for _, peer := range e.peers {
		wgPeer, loaded := wgDevice.LookupActivePeer(peer.publicKey)
		if loaded {
			wgPeers = append(wgPeers, wgPeer)
		}
	}
	return wgDevice, wgPeers, nil
}

// publishLocked makes a built chain the endpoint's live chain. It must be called with stateAccess
// held and after the caller re-checked `closing`.
func (e *Endpoint) publishLocked(wgDevice *device.Device, wgPeers []*device.Peer) {
	e.tunDevice.SetDevice(wgDevice, wgPeers)
	e.device.Store(wgDevice)
	e.everPublished = true
	e.deferred = false
	e.addressingReady = true
}

// detachLocked removes the live chain from the endpoint and detaches the peer routes, so no packet
// from a flow that stays in the stack can reach the Device that is about to be closed. It returns
// the Device (nil when none was live); the caller closes it outside the lock.
func (e *Endpoint) detachLocked() *device.Device {
	wgDevice := e.device.Swap(nil)
	if wgDevice == nil {
		return nil
	}
	if provider, isProvider := e.tunDevice.(attachmentProvider); isProvider {
		provider.clearDevice()
	}
	return wgDevice
}

// MinimumIPv6TunnelMTU is the smallest inner IP MTU over which IPv6 can be carried, from RFC 8200
// section 5: every link on an IPv6 path must carry 1280 bytes, and a tunnel interface is a link.
//
// It is exported because protocol/wireguard has to name it too: a nested endpoint's MTU can be bounded
// by a detour's capacity, and the remedy it reports is the detour MTU that would prove this much
// capacity. One definition, read from both places, is the point.
const MinimumIPv6TunnelMTU = 1280

// minimumIPv6TunnelMTU is the in-package reading of the exported constant above, kept so the validation
// below and its tests read as they did before the export.
const minimumIPv6TunnelMTU = MinimumIPv6TunnelMTU

// validateTunnelMTU refuses a tunnel whose MTU cannot carry the addresses it is configured with.
//
// # The two cases that must NOT be conflated
//
// An MTU below 1280 is not universally illegal - it is illegal FOR IPv6, and it is a legal special
// case for an IPv4-only tunnel, where a small MTU is a legitimate way to fit a narrow path. A blanket
// floor would reject those configurations, which is why this refuses exactly one combination: an IPv6
// address configured on a tunnel whose MTU cannot carry IPv6.
//
// # What the refusal replaces
//
// Today such a configuration is accepted, and then fails per flow: sing-tun's dispatcher refuses an
// IPv6 flow whose port reports an MTU below header.IPv6MinimumMTU
// (flow_dispatch.go:502-505, `if packet.ipVersion == 6 && effectiveMTU != 0 && effectiveMTU <
// header.IPv6MinimumMTU { return nil, createFlowUnsupported }`), so the operator sees a flow that is
// "unsupported" rather than an MTU that is too small, and IPv4 through the same endpoint keeps
// working - which makes the real cause very hard to see. The address the operator configured is
// unreachable by construction, so the honest place to say so is construction.
//
// # Why the address list, and not the MTU alone
//
// `Address` is the tunnel's own address set, and it is what the endpoint hands to the device and
// judges flows against. An IPv6 prefix there means this endpoint is expected to carry IPv6; no IPv6
// prefix means it is an IPv4-only tunnel, whatever the MTU, and it is left exactly as it was.
func validateTunnelMTU(
	mtu uint32,
	addresses []netip.Prefix,
	// boundedBy names the `detour` whose proven capacity produced the MTU, and is empty when the value
	// is the operator's own. It changes the ADVICE, never the decision: both cases are refused, because
	// an IPv6 address is unreachable on a tunnel below 1280 whichever way the number was arrived at.
	boundedBy string,
	// boundedRequired is the `mtu` the detour would need in order to prove a capacity large enough for
	// this tunnel to carry IPv6, and is meaningful only when boundedBy is set. It is handed in rather
	// than re-derived here: the header lengths and the detour's own encapsulation are common/dialer's
	// arithmetic, and a second derivation in this package would be a second thing to keep in step.
	boundedRequired uint32,
) error {
	if mtu >= minimumIPv6TunnelMTU {
		return nil
	}
	for _, prefix := range addresses {
		if !prefix.Addr().Is6() || prefix.Addr().Is4In6() {
			continue
		}
		if boundedBy != "" {
			// The one remediation the operator cannot perform is the one the generic message leads
			// with, so it must not be offered here at all: `mtu` is derived from `detour`, and a larger
			// configured value is clamped straight back to this same number. The reachable levers are
			// named instead, and the detour's own number is given as a value rather than as a
			// description of one, because it is the thing the operator has to go and change.
			//
			// MEASURED: a detour with a 1210-byte inner MTU produces a 1130-byte nested MTU, and the
			// detour would need 1210 + (1280 - 1130) = 1360 bytes of its own for this tunnel to carry
			// the address.
			return E.New("`mtu` ", mtu, " cannot carry the IPv6 address ", prefix.Addr(),
				": an IPv6 path requires at least ", minimumIPv6TunnelMTU,
				" bytes (RFC 8200 section 5), so the configured address is unreachable. ",
				"This MTU was not configured: it is the capacity detour `", boundedBy,
				"` proves for a tunnel nested inside it, so raising `mtu` cannot help - any larger value ",
				"is clamped back to ", mtu, ". Raise the detour's own `mtu` to at least ",
				boundedRequired, " bytes, or remove the IPv6 address to run an IPv4-only tunnel at ",
				"this MTU")
		}
		return E.New("`mtu` ", mtu, " cannot carry the IPv6 address ", prefix.Addr(),
			": an IPv6 path requires at least ", minimumIPv6TunnelMTU,
			" bytes (RFC 8200 section 5), so the configured address is unreachable. ",
			"Raise `mtu` to at least ", minimumIPv6TunnelMTU,
			", or remove the IPv6 address to run an IPv4-only tunnel at this MTU")
	}
	return nil
}

// bindListenPort brings the device up and makes a configured `listen_port` a fact of the running
// endpoint rather than a line of text in the IPC configuration.
//
// # The two failures this closes
//
// wireguard-go opens the bind from the UP transition, not from IpcSet: `BindUpdate` closes the
// existing sockets and then returns immediately when the device is not up, and the transition that
// actually opens them is driven by the tun device's event channel - an ASYNCHRONOUS goroutine
// (`device.RoutineTUNEventReader`). So `listen_port=N` reached `device.net.port` and the bind was
// opened later, off whichever goroutine happened to observe the event. Two things followed:
//
//  1. Start returned before the socket existed. The port was still free after a successful Start, so
//     the endpoint was published as ready while nothing could receive on it, and anything that
//     checked the port - a monitoring probe, a second instance starting on the same port - got the
//     answer "free" from an endpoint that was about to take it.
//  2. A bind failure was SWALLOWED. When the port was already held by another process, `IpcSet`
//     succeeded (its own BindUpdate was a no-op on a device that was not up yet), Start returned nil,
//     and the failure surfaced only later as a logged "Unable to update bind" while the device fell
//     back to down. The endpoint then had no socket at all and no error to the operator:
//     `listen_port` was configured, silently not honoured, and the tunnel was dead.
//
// # The order, and why it is this order
//
//  1. Up() - the bind is opened HERE, synchronously, and its error is returned. This is the only
//     state in which wireguard-go opens sockets, so a port that cannot be bound fails Start.
//  2. With a pinned port, read back the port the device reports and, when it is not the configured
//     one, re-apply the pin. The device's asynchronous up transition can interleave with the
//     `listen_port` line of IpcSet, and `netc.port` is assigned from the bind's own return value, so
//     a concurrent open with port 0 could replace the pinned port with an ephemeral one - silently,
//     with no error anywhere. Re-applying the pin is deterministic precisely because it runs after
//     the device is up; it happens only when the port is wrong, so the normal path opens one bind.
//  3. Verify. A dialer that cannot own a listening socket at all - ClientBind, which is the bind for a
//     detour and has no local port of its own - reports actualPort 0 instead of failing. That is a
//     capability boundary rather than a transient error, so it is reported as one: the operator asked
//     for a listening port and this endpoint cannot provide one.
//
// It deliberately does not substitute another dialer to make the bind succeed. The dialer carries the
// operator's detour, bind_interface and routing mark, and opening a listening socket on a path the
// operator did not choose would be a worse failure than refusing to start.
func (e *Endpoint) bindListenPort(wgDevice *device.Device) error {
	err := wgDevice.Up()
	if err != nil {
		return E.Cause(err, "bring up wireguard device")
	}
	actualPort := e.currentListenPort(wgDevice)
	if e.options.ListenPort == 0 {
		// Dynamic allocation: there is nothing to verify. The port the kernel chose is still worth
		// reporting, because it is the one fact about this endpoint an operator cannot otherwise see.
		if actualPort != 0 {
			e.options.Logger.Info("wireguard[", e.options.Tag, "] listening on port ", actualPort)
		}
		return nil
	}
	if actualPort != e.options.ListenPort {
		err = wgDevice.IpcSet("listen_port=" + F.ToString(e.options.ListenPort) + "\n")
		if err != nil {
			return E.Cause(err, "bind listen_port ", e.options.ListenPort)
		}
		actualPort = e.currentListenPort(wgDevice)
		if actualPort != e.options.ListenPort {
			return E.New("listen_port ", e.options.ListenPort,
				" is not bound: this endpoint's dialer cannot own a listening socket (the device reports port ",
				actualPort, ")")
		}
	}
	e.options.Logger.Info("wireguard[", e.options.Tag, "] listening on port ", actualPort)
	return nil
}

func (e *Endpoint) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if !destination.Addr.IsValid() {
		return nil, E.Cause(os.ErrInvalid, "invalid non-IP destination")
	}
	if err := e.resumeForCaller(ctx); err != nil {
		return nil, err
	}
	release, err := e.beginUse(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	conn, dialErr := e.tunDevice.DialContext(ctx, network, destination)
	if dialErr != nil {
		return nil, dialErr
	}
	if N.NetworkName(network) == N.NetworkUDP {
		return e.trackUDPAssociation(conn), nil
	}
	return conn, nil
}

// beginUse is the flow-creation side of the release protocol.
//
// It holds buildAccess across BOTH the (possible) rebuild and the increment of activeUses. That
// ordering is the whole mutual exclusion: a suspend also takes buildAccess before it may release
// anything, so it can never observe "no device and no use" in the window between a rebuild
// publishing a device and the dial starting to use it.
func (e *Endpoint) beginUse(ctx context.Context) (func(), error) {
	e.buildAccess.Lock()
	defer e.buildAccess.Unlock()

	e.stateAccess.Lock()
	if e.closing {
		e.stateAccess.Unlock()
		return nil, os.ErrClosed
	}
	if wgDevice := e.device.Load(); wgDevice != nil {
		if e.suspended.Load() {
			e.suspended.Store(false)
			if !e.networkPaused {
				wgDevice.Up()
			}
		}
		e.activeUses++
		e.stateAccess.Unlock()
		return e.releaseUse, nil
	}
	everPublished, deferred := e.everPublished, e.deferred
	if !everPublished {
		// Never started: the stack is not running either, so keep the endpoint's previous error
		// surface (the dial fails from the stack, exactly as it did before this mechanism).
		e.activeUses++
		e.stateAccess.Unlock()
		return e.releaseUse, nil
	}
	if deferred && adapter.IsBackgroundProbe(ctx) {
		// A released chain is a suspended resource: the probe must not be what rebuilds it.
		e.stateAccess.Unlock()
		return nil, adapter.ErrResourceSuspended
	}
	// Demand: a caller that is not a background probe resumes the endpoint and pays the rebuild.
	e.suspended.Store(false)
	hook := e.buildDeviceHook
	e.stateAccess.Unlock()

	if hook != nil {
		if hookErr := hook(); hookErr != nil {
			return nil, E.Cause(hookErr, "rebuild wireguard device")
		}
	}
	bind, err := e.newBind()
	if err != nil {
		return nil, err
	}
	wgDevice, wgPeers, err := e.buildDevice(bind)
	if err != nil {
		return nil, err
	}
	e.stateAccess.Lock()
	if e.closing {
		e.stateAccess.Unlock()
		wgDevice.Close()
		return nil, os.ErrClosed
	}
	e.publishLocked(wgDevice, wgPeers)
	if e.networkPaused {
		wgDevice.Down()
	}
	e.activeUses++
	e.stateAccess.Unlock()
	// Same ordering as Start: after the initial IpcSet, before any traffic drives a retry.
	wgDevice.SetSessionStateFunc(e.sessionStateChanged)
	e.options.Logger.Debug("wireguard[", e.options.Tag, "] rebuilt the wireguard-go device on demand")
	return e.releaseUse, nil
}

func (e *Endpoint) releaseUse() {
	e.stateAccess.Lock()
	e.activeUses--
	e.stateAccess.Unlock()
}

// acquireDevice hands out the CURRENT device for a caller that must not wake a released chain (a
// rebind or an inbound packet write). It returns nil when no device is published, which every such
// caller already had to handle.
func (e *Endpoint) acquireDevice() (*device.Device, func(), error) {
	e.stateAccess.Lock()
	if e.closing {
		e.stateAccess.Unlock()
		return nil, nil, os.ErrClosed
	}
	wgDevice := e.device.Load()
	if wgDevice == nil {
		e.stateAccess.Unlock()
		return nil, nil, nil
	}
	e.activeUses++
	e.stateAccess.Unlock()
	return wgDevice, e.releaseUse, nil
}

// resumeForCaller wakes a suspended endpoint only for a caller that is allowed to.
//
// # Why a background probe may not wake it
//
// A suspended endpoint has been released deliberately - nothing references it, or the device is
// paused - so its device is down. A periodic health check that woke it would spin up a tunnel
// engine for a measurement, which is the opposite of what the idle policy is for, and it would also
// mean the device never stays idle. The probe therefore fails, with a sentinel the health layer
// recognises, and the endpoint's existing health evidence is left alone: "not measured" is not
// "unhealthy".
//
// Real device traffic never reaches here. A flow the device asked for is handed to the endpoint's
// own tun path, which is where demand wakes it.
func (e *Endpoint) resumeForCaller(ctx context.Context) error {
	if !e.suspended.Load() {
		return nil
	}
	if adapter.IsBackgroundProbe(ctx) {
		return adapter.ErrResourceSuspended
	}
	e.resume()
	return nil
}

func (e *Endpoint) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	if !destination.Addr.IsValid() {
		return nil, E.Cause(os.ErrInvalid, "invalid non-IP destination")
	}
	if err := e.resumeForCaller(ctx); err != nil {
		return nil, err
	}
	release, err := e.beginUse(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	conn, listenErr := e.tunDevice.ListenPacket(ctx, destination)
	if listenErr != nil {
		return nil, listenErr
	}
	return e.trackUDPAssociationPacket(conn), nil
}

// SetIdle applies the idle policy. true is the suspend path: the Device goes down and, for the
// eligible shape, the heavy chain is released entirely. false only clears the suspension (and brings
// a still-published device up); a released chain is rebuilt on the next real demand, which is what
// makes the release useful instead of a costly stop/start on every pause boundary.
func (e *Endpoint) SetIdle(idle bool) {
	if idle {
		e.suspend()
	} else {
		e.resume()
	}
}

// suspend is the only reclamation point for the heavy chain.
//
// The pre-existing behaviour (Device.Down, keeping the Device and its 8.4 MB TUN-reader batch
// resident) is preserved whenever the release would not be safe:
//   - the shape is not eligible (listen port / keepalive / system / passive or domain peer);
//   - a flow-creation call is in flight (activeUses > 0);
//   - a return path is attached, so inbound packets would need the Device that is being closed;
//   - a Close already won.
func (e *Endpoint) suspend() {
	e.buildAccess.Lock()
	defer e.buildAccess.Unlock()
	e.stateAccess.Lock()
	if e.suspended.Load() {
		e.stateAccess.Unlock()
		return
	}
	e.suspended.Store(true)
	wgDevice := e.device.Load()
	if wgDevice == nil {
		e.stateAccess.Unlock()
		return
	}
	wgDevice.Down()
	release := e.lazyEligible && !e.closing && e.activeUses == 0 && e.returnPath.Load() == nil &&
		e.udpAssociations.Load() == 0
	if !release {
		e.stateAccess.Unlock()
		return
	}
	wgDevice = e.detachLocked()
	e.deferred = true
	e.stateAccess.Unlock()
	// Close outside stateAccess: it waits for the device's own goroutines, and a pause callback
	// that wants the lock must not be blocked behind that wait.
	wgDevice.Close()
	e.options.Logger.Debug("wireguard[", e.options.Tag, "] suspended: released the wireguard-go device (the stack and its flows are kept)")
}

func (e *Endpoint) resume() {
	e.stateAccess.Lock()
	defer e.stateAccess.Unlock()
	if !e.suspended.Load() {
		return
	}
	e.suspended.Store(false)
	wgDevice := e.device.Load()
	if wgDevice == nil {
		// A released chain: rebuilt on demand, not here. Waking it eagerly would spend the Device's
		// 8.4 MB for a network event that may carry no traffic. The demand that DOES justify the
		// rebuild is a packet from a flow that stayed in the stack; see rebuildOnTrafficDemand.
		return
	}
	if !e.networkPaused {
		wgDevice.Up()
	}
}

// rebuildOnTrafficDemand restores a released heavy chain because a flow that survived the release
// produced traffic again.
//
// # Why this is not beginUse
//
// beginUse answers a NEW flow, so it increments activeUses and refuses a background probe. This path
// answers a flow that already exists: there is no new dial to account for and no probe context to
// consult - a background probe cannot reach it, because a probe opens its own flow and goes through
// beginUse. What it shares with beginUse is the protocol: buildAccess for the whole rebuild (so a
// concurrent suspend cannot release a chain that is being rebuilt), stateAccess only for the checks
// and the publish, and the same closing re-check before anything is published.
//
// # What it deliberately does not do
//
// It does not run while the endpoint is still suspended. A packet can only appear here after the
// caller resumed the endpoint (that is what cleared the suspension), and rebuilding during a real
// suspend - a paused network, an app that keeps retransmitting - would spend the Device on traffic
// that cannot leave the machine anyway. It also does not hold the packet that triggered it: the
// packet is dropped by the caller and the flow's retransmission is the one that arrives.
func (e *Endpoint) rebuildOnTrafficDemand() {
	e.buildAccess.Lock()
	defer e.buildAccess.Unlock()

	e.stateAccess.Lock()
	if e.closing || e.device.Load() != nil || !e.deferred || e.suspended.Load() {
		e.stateAccess.Unlock()
		return
	}
	hook := e.buildDeviceHook
	e.stateAccess.Unlock()

	if hook != nil {
		if hookErr := hook(); hookErr != nil {
			e.options.Logger.Debug("wireguard[", e.options.Tag, "] rebuild on traffic failed: ", hookErr)
			return
		}
	}
	bind, err := e.newBind()
	if err != nil {
		e.options.Logger.Debug("wireguard[", e.options.Tag, "] rebuild on traffic failed: ", err)
		return
	}
	wgDevice, wgPeers, err := e.buildDevice(bind)
	if err != nil {
		e.options.Logger.Debug("wireguard[", e.options.Tag, "] rebuild on traffic failed: ", err)
		return
	}
	e.stateAccess.Lock()
	if e.closing {
		e.stateAccess.Unlock()
		wgDevice.Close()
		return
	}
	e.publishLocked(wgDevice, wgPeers)
	if e.networkPaused {
		wgDevice.Down()
	}
	e.stateAccess.Unlock()
	wgDevice.SetSessionStateFunc(e.sessionStateChanged)
	e.options.Logger.Debug("wireguard[", e.options.Tag, "] rebuilt the release wireguard-go device for a retained flow")
}

func (e *Endpoint) Close() error {
	// The closing flag is set under the lock every publisher takes. A build that is still running
	// when Close lands is NOT waited for here - the Start path is deliberately not blocked behind the
	// expensive build - it re-checks `closing` under stateAccess at publish time and closes what it
	// built instead of publishing. That check is the whole "no late publish" guarantee; a build takes
	// nothing from the stack except its own attachment, so it cannot resurrect a closed endpoint.
	e.closeRecovery()
	if e.removeRegistration != nil {
		e.removeRegistration()
		e.removeRegistration = nil
	}
	// The handles are taken OUT of the endpoint under the lock, and released to the manager only
	// after it is dropped. Unregistering inside the critical section is the other half of the ABBA
	// cycle described at the registration site: `UnregisterCallback` needs `pause.access`, which a
	// concurrent `emit` holds while it waits for `stateAccess`. Clearing the fields under the lock
	// still guarantees exactly-one unregister, including for a repeated Close.
	e.stateAccess.Lock()
	e.closing = true
	pauseCallback := e.pauseCallback
	deviceWakeCallback := e.deviceWakeCallback
	e.pauseCallback = nil
	e.deviceWakeCallback = nil
	wgDevice := e.device.Swap(nil)
	if wgDevice != nil {
		wgDevice.Down()
	}
	e.stateAccess.Unlock()
	if wgDevice != nil {
		// Outside the lock: Device.Close waits for every goroutine it owns.
		wgDevice.Close()
	}

	// Released to the manager only now that `stateAccess` is dropped. See the registration site:
	// `UnregisterCallback` needs `pause.access`, which a concurrent `emit` holds while it waits for
	// `stateAccess`, so unregistering inside the critical section would close the same cycle from
	// the other side.
	pauseManager := e.pause
	if pauseManager != nil {
		if pauseCallback != nil {
			pauseManager.UnregisterCallback(pauseCallback)
		}
		if deviceWakeCallback != nil {
			pauseManager.UnregisterCallback(deviceWakeCallback)
		}
	}

	if e.egressPool != nil {
		e.egressPool.Close()
		e.egressPool = nil
	}
	// The stack/tun is closed exactly once, whether or not a Device was ever published. For the
	// lazy shape the Device's Close only detached its attachment, so this is what releases the
	// stack; for the other Device kinds their own Close already ran and their close is idempotent.
	return common.Close(e.tunDevice)
}

// lazyState reports whether a heavy chain is published and whether it was released by suspend. It
// exists for the lifecycle tests and diagnostics, which must distinguish "never built" from
// "released" without racing the publish.
func (e *Endpoint) lazyState() (published bool, released bool) {
	e.stateAccess.Lock()
	defer e.stateAccess.Unlock()
	return e.device.Load() != nil, e.deferred
}

// Lookup answers from configuration, not from the live Device, so the answer is identical before
// Start, while published, while suspended, and after a rebuild.
//
// Exact previous semantics: nil before the first publish and after Close (there was no address
// table then, and the Device's trie is emptied by its Close); the owning peer for a covered address
// in between. The returned *device.Peer is a stable per-config key - the product only compares it
// with nil (protocol/wireguard PreferredAddress), and its identity was never part of the answer.
func (e *Endpoint) Lookup(address netip.Addr) *device.Peer {
	e.stateAccess.Lock()
	ready := e.addressingReady && !e.closing
	e.stateAccess.Unlock()
	if !ready || e.staticAllowedIPs == nil {
		return nil
	}
	return e.staticAllowedIPs.LookupFromPacket(netip.Addr{}, address, nil)
}

func (e *Endpoint) BindUpdate() error {
	wgDevice, release, err := e.acquireDevice()
	if err != nil {
		return err
	}
	if wgDevice == nil {
		return nil
	}
	defer release()
	return wgDevice.BindUpdate()
}

// onPauseUpdated applies a network pause or wake to the live device.
//
// # Why this is synchronous, and why that is the constraint the fix had to respect
//
// A pause is a resource contract, not a notification: `EventNetworkPause` must have RELEASED the
// device's UDP socket by the time the manager's `DevicePause()` returns, because the platform
// suspend path relies on it and `TestPauseWakeCyclesInterleavedWithRebindsKeepOneSocket` asserts
// the release at the boundary with no grace period. Deferring this to a queue would turn that
// contract into a race.
//
// So the deadlock this callback used to take part in is NOT broken here: it is broken at the
// registration sites, by keeping `stateAccess` out of the calls into the pause manager. See the
// comment at `RegisterCallback` in startDevice.
func (e *Endpoint) onPauseUpdated(event int) {
	e.stateAccess.Lock()
	defer e.stateAccess.Unlock()
	wgDevice := e.device.Load()
	if wgDevice == nil {
		return
	}
	switch event {
	case pause.EventNetworkPause:
		e.networkPaused = true
		wgDevice.Down()
	case pause.EventNetworkWake:
		e.networkPaused = false
		if !e.suspended.Load() {
			wgDevice.Up()
		}
	}
}

// onDeviceWake is the consumer's nudge (trigger 3 of the recovery contract).
//
// # Why this is a separate callback from onPauseUpdated
//
// They are different events with different meanings. EventNetworkWake says the routing environment
// came back; EventDeviceWake says the DEVICE woke - the foreground/background transition the Apple
// and Android clients drive through PauseManager.DeviceWake. A tunnel that slept through a device
// wake is precisely the field case LX 041 was written for: the handshake is still retrying into a
// flow the sleep destroyed, and the user is looking at the screen.
//
// The nudge applies the same stale predicate as the other triggers, so a healthy session costs
// nothing, and it skips a suspended endpoint, so it cannot spin up a tunnel the idle policy
// released.
func (e *Endpoint) onDeviceWake(event int) {
	switch event {
	case pause.EventDeviceWake:
		go e.rebindOnWake()
	}
}

type peerConfig struct {
	destination     M.Socksaddr
	endpoint        netip.AddrPort
	publicKey       device.NoisePublicKey
	preSharedKeyHex string
	allowedIPs      []netip.Prefix
	keepalive       uint16
	reserved        [3]uint8
}

func (c peerConfig) GenerateIpcLines() string {
	var ipcLines strings.Builder
	ipcLines.WriteString("\npublic_key=" + hex.EncodeToString(c.publicKey[:]))
	if c.endpoint.IsValid() {
		ipcLines.WriteString("\nendpoint=" + c.endpoint.String())
	}
	if c.preSharedKeyHex != "" {
		ipcLines.WriteString("\npreshared_key=" + c.preSharedKeyHex)
	}
	for _, allowedIP := range c.allowedIPs {
		ipcLines.WriteString("\nallowed_ip=" + allowedIP.String())
	}
	if c.keepalive > 0 {
		ipcLines.WriteString("\npersistent_keepalive_interval=" + F.ToString(c.keepalive))
	}
	return ipcLines.String()
}

// udpAssociationConn marks one open UDP association for the release boundary.
//
// The wrapper is deliberately transparent: callers keep the net.Conn/net.PacketConn they were given,
// and only Close is intercepted, because that is the moment the association stops needing the Device.
type udpAssociationConn struct {
	net.Conn
	endpoint *Endpoint
	once     sync.Once
}

func (c *udpAssociationConn) Close() error {
	c.once.Do(func() { c.endpoint.udpAssociations.Add(-1) })
	return c.Conn.Close()
}

type udpAssociationPacketConn struct {
	net.PacketConn
	endpoint *Endpoint
	once     sync.Once
}

func (c *udpAssociationPacketConn) Close() error {
	c.once.Do(func() { c.endpoint.udpAssociations.Add(-1) })
	return c.PacketConn.Close()
}

// trackUDPAssociation counts the association BEFORE returning it, so a suspend racing the caller
// cannot observe "no association" and release the Device under it.
func (e *Endpoint) trackUDPAssociation(conn net.Conn) net.Conn {
	e.udpAssociations.Add(1)
	return &udpAssociationConn{Conn: conn, endpoint: e}
}

func (e *Endpoint) trackUDPAssociationPacket(conn net.PacketConn) net.PacketConn {
	e.udpAssociations.Add(1)
	return &udpAssociationPacketConn{PacketConn: conn, endpoint: e}
}
