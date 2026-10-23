module test

go 1.25.5

require github.com/sagernet/sing-box v0.0.0

replace github.com/sagernet/sing-box => ../

require (
	github.com/coder/websocket v1.8.14
	github.com/docker/docker v27.3.1+incompatible
	github.com/docker/go-connections v0.5.0
	github.com/gofrs/uuid/v5 v5.5.1
	github.com/miekg/dns v1.1.72
	github.com/opencontainers/image-spec v1.1.0
	github.com/sagernet/quic-go v0.61.0-sing-box-mod.9
	github.com/sagernet/sing v0.9.7-0.20260929150544-6f21f2425a95
	github.com/sagernet/sing-anytls v0.0.0-20260928104022-580984e4d8cb
	github.com/sagernet/sing-openvpn v0.1.1-0.20261020061844-7d24ecd92837
	github.com/sagernet/sing-quic v0.7.2-0.20260929152029-258509488380
	github.com/sagernet/sing-shadowsocks v0.2.9-0.20260929152116-0a3456819ce7
	github.com/sagernet/sing-shadowsocks2 v0.2.2-0.20260929152114-a69d1086332b
	github.com/sagernet/sing-tun v0.9.7-0.20261013140216-9ffa5391a2ad
	github.com/sagernet/tailscale v1.102.1-sing-box-1.14-mod.5.0.20260925112514-35e61219dedd
	github.com/spyzhov/ajson v0.9.4
	github.com/stretchr/testify v1.12.0
	go.uber.org/goleak v1.3.0
	go4.org/mem v0.0.0-20240501181205-ae6ca9944745
	golang.org/x/net v0.57.0
	google.golang.org/protobuf v1.36.11
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20210617225240-d185dfc1b5a1 // indirect
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/RyuaNerin/go-krypto v1.3.0 // indirect
	github.com/ajg/form v1.5.1 // indirect
	github.com/akutz/memconn v0.1.0 // indirect
	github.com/alexbrainman/sspi v0.0.0-20231016080023-1a75b4708caa // indirect
	github.com/anchore/go-lzo v0.1.0 // indirect
	github.com/andybalholm/brotli v1.1.0 // indirect
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/anthropics/anthropic-sdk-go v1.26.0 // indirect
	github.com/axiomhq/hyperloglog v0.0.0-20240319100328-84253e514e02 // indirect
	github.com/caddyserver/certmagic v0.25.3-0.20260421143802-60d9d8b415d6 // indirect
	github.com/caddyserver/zerossl v0.1.5 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/coreos/go-iptables v0.7.1-0.20240112124308-65c67c9f46e6 // indirect
	github.com/coreos/go-oidc/v3 v3.17.0 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/cretz/bine v0.2.0 // indirect
	github.com/database64128/netx-go v0.1.1 // indirect
	github.com/database64128/tfo-go/v2 v2.3.2 // indirect
	github.com/dblohm7/wingoes v0.0.0-20240119213807-a09d6be7affa // indirect
	github.com/dgryski/go-camellia v0.0.0-20191119043421-69a8a13fb23d // indirect
	github.com/dgryski/go-metro v0.0.0-20180109044635-280f6062b5bc // indirect
	github.com/distribution/reference v0.5.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/ebitengine/purego v0.10.0 // indirect
	github.com/enfein/mieru/v3 v3.38.1 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/florianl/go-nfqueue/v2 v2.1.0 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/gaissmai/bart v0.26.1 // indirect
	github.com/go-chi/chi/v5 v5.2.5 // indirect
	github.com/go-chi/render v1.0.3 // indirect
	github.com/go-jose/go-jose/v4 v4.1.6 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go4org/hashtriemap v0.0.0-20251130024219-545ba229f689 // indirect
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/google/certificate-transparency-go v1.3.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/gopacket v1.1.19 // indirect
	github.com/google/nftables v0.2.1-0.20240414091927-5e242ec57806 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/yamux v0.1.2 // indirect
	github.com/hdevalence/ed25519consensus v0.2.0 // indirect
	github.com/huin/goupnp v1.3.0 // indirect
	github.com/insomniacslk/dhcp v0.0.0-20260220084031-5adc3eb26f91 // indirect
	github.com/jackpal/go-nat-pmp v1.0.2 // indirect
	github.com/jsimonetti/rtnetlink v1.4.1 // indirect
	github.com/keybase/go-keychain v0.0.1 // indirect
	github.com/klauspost/compress v1.19.1 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/koron/go-ssdp v0.0.4 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/libdns/acmedns v0.5.0 // indirect
	github.com/libdns/alidns v1.0.6 // indirect
	github.com/libdns/cloudflare v0.2.2 // indirect
	github.com/libdns/libdns v1.1.1 // indirect
	github.com/libp2p/go-nat v1.0.1-0.20250821073202-01afc089f138 // indirect
	github.com/libp2p/go-netroute v0.2.1 // indirect
	github.com/logrusorgru/aurora v2.0.3+incompatible // indirect
	github.com/mdlayher/netlink v1.11.2 // indirect
	github.com/mdlayher/socket v0.6.0 // indirect
	github.com/metacubex/utls v1.8.7 // indirect
	github.com/mholt/acmez/v3 v3.1.6 // indirect
	github.com/mitchellh/go-ps v1.0.0 // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/moby/term v0.5.0 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/openai/openai-go/v3 v3.26.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.26 // indirect
	github.com/pion/dtls/v3 v3.1.5 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/transport/v4 v4.0.2 // indirect
	github.com/pires/go-proxyproto v0.8.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pkg/sftp v1.13.10 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/safchain/ethtool v0.3.0 // indirect
	github.com/sagernet/bbolt v0.0.0-20260915102804-500ee1e84832 // indirect
	github.com/sagernet/cors v1.2.1 // indirect
	github.com/sagernet/cronet-go v0.0.0-20260929213745-4d18a60dc3a8 // indirect
	github.com/sagernet/cronet-go/all v0.0.0-20260929213745-4d18a60dc3a8 // indirect
	github.com/sagernet/cronet-go/lib/android_386 v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/android_amd64 v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/android_arm v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/android_arm64 v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/darwin_amd64 v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/darwin_arm64 v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/ios_amd64_simulator v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/ios_arm64 v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/ios_arm64_simulator v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_386 v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_386_musl v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_amd64 v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_amd64_musl v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_arm v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_arm64 v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_arm64_musl v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_arm_musl v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_loong64 v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_loong64_musl v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_mips64le v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_mipsle v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_mipsle_musl v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_riscv64 v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/linux_riscv64_musl v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/tvos_amd64_simulator v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/tvos_arm64 v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/tvos_arm64_simulator v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/windows_amd64 v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/cronet-go/lib/windows_arm64 v0.0.0-20260929213014-a1cafd93eb1f // indirect
	github.com/sagernet/fswatch v0.1.2 // indirect
	github.com/sagernet/gliderssh v0.3.4-0.20260531100337-2194faca5648 // indirect
	github.com/sagernet/gvisor v0.0.0-20260727.0-sing-box-mod.1 // indirect
	github.com/sagernet/netlink v0.0.0-20260814022025-64455d367bbf // indirect
	github.com/sagernet/nftables v0.3.0-mod.4 // indirect
	github.com/sagernet/sing-cloudflared v0.1.4-0.20261021152903-2d40d788fcf6 // indirect
	github.com/sagernet/sing-mux v0.3.10-0.20260929204512-caf09fe32475 // indirect
	github.com/sagernet/sing-openconnect v0.1.1-0.20260929151226-29757281a247 // indirect
	github.com/sagernet/sing-shadowtls v0.2.2-0.20260928201441-a9c0127d5c99 // indirect
	github.com/sagernet/sing-snell v0.0.0-20260904135315-bc5a12ac736f // indirect
	github.com/sagernet/sing-usbip v0.0.0-20260817040617-28bd42667eca // indirect
	github.com/sagernet/sing-vmess v0.2.9-0.20260929152519-9b95ab8c9478 // indirect
	github.com/sagernet/smux v1.5.50-sing-box-mod.1 // indirect
	github.com/sagernet/wireguard-go v0.0.8-0.20260929150556-ca3bc60c4ce7 // indirect
	github.com/sagernet/ws v0.0.0-20231204124109-acfe8907c854 // indirect
	github.com/smallstep/pkcs7 v0.1.1 // indirect
	github.com/tailscale/certstore v0.1.1-0.20260409135935-3638fb84b77d // indirect
	github.com/tailscale/go-winio v0.0.0-20231025203758-c4f33415bf55 // indirect
	github.com/tailscale/hujson v0.0.0-20260302212456-ecc657c15afd // indirect
	github.com/tailscale/netlink v1.1.1-0.20240822203006-4d49adab4de7 // indirect
	github.com/tailscale/peercred v0.0.0-20250107143737-35a0c7bd7edc // indirect
	github.com/tailscale/web-client-prebuilt v0.0.0-20250124233751-d4cd19a26976 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/tjfoc/gmsm v1.4.1 // indirect
	github.com/u-root/uio v0.0.0-20240224005618-d2acac8f3701 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	github.com/zeebo/blake3 v0.2.4 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.59.0 // indirect
	go.opentelemetry.io/otel v1.39.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.31.0 // indirect
	go.opentelemetry.io/otel/metric v1.39.0 // indirect
	go.opentelemetry.io/otel/trace v1.39.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.1 // indirect
	go.uber.org/zap/exp v0.3.0 // indirect
	go4.org/netipx v0.0.0-20231129151722-fdeea329fbba // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/exp v0.0.0-20260410095643-746e56fc9e2f // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.47.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
	golang.zx2c4.com/wireguard/windows v0.5.3 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251202230838-ff82c1b0f217 // indirect
	google.golang.org/grpc v1.79.3 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	gotest.tools/v3 v3.5.1 // indirect
	lukechampine.com/blake3 v1.3.0 // indirect
	zombiezen.com/go/capnproto2 v2.18.2+incompatible // indirect
)

// Same fork revision as the root module. The nested module does NOT inherit the root's
// replaces, and this pin used to lag one commit behind (dc9f4ea02e02, which lacks the
// WriteOwnedBuffer first-payload ownership handoff the socks early-data path uses). A test module
// pinned to a different revision of the code under test verifies a different object, so the two
// are kept identical and scripts/ci/check-go-module-integrity.sh fails when they drift.
//
// The current revision also carries the bufio splice fix (a Pipe2 setup failure must fall back
// rather than claim the handover); the root's pin comment names the commit and the reason.
replace github.com/sagernet/sing => github.com/Piggy-Cat-bit-shadow/sing v0.9.6-0.20261019051552-baed8c3b4a34

// Adds the accept-loop recovery (040), the Go stack's Start/Close handshake, and the IPv6-only
// re-bind self-heal. Same fork revision as the root module: the test module runs integration tests
// against the TUN stack, so without this replace it linked UPSTREAM sing-tun and the Start/Close
// race fix under test was not the code being tested. See the root go.mod for the fork's base and
// branch.
//
// This pin was one commit behind the root again - 8dde9c8cbe27 while the root built 6937951ec9b6 -
// and the drift was caught by scripts/ci/check-go-module-integrity.sh on the Final Freeze run for
// v0.1.8, which is exactly the "a nested module pinned to a different revision of the code under
// test verifies a different object" failure this replace's comment above warns about. The require
// line moved with it (sing-tun's own base revision 9ffa5391a2ad, the same one the root requires),
// because a replace changes which revision is built while the require line names the module's
// declared base; leaving them split is what made `go list -mod=readonly -m all` refuse to resolve
// the module graph at all.
//
// The d092c64 bump moved the root and this module TOGETHER, which is the whole point of the guard
// above: the audit branch that produced the fix (audit/dual-stack-ipv4-ipv6-lifecycle @ 200274c5076)
// changed only the root, so as it stood the root built d092c64 while this module still pinned
// 6937951ec9b6 - the same root-vs-nested drift, and check-go-module-integrity.sh would have failed
// it. The require line still names the same base (9ffa5391a2ad) because the pin's base did not move.
replace github.com/sagernet/sing-tun => github.com/Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261023224106-1d689640cfb4

// Same fork revision as the root module, for the same reason as every other pin here: a nested
// module does not inherit the root's replaces, so a test binary built from this module would
// otherwise exercise a different QUIC stack than the one that ships. The revision adds
// InitialPacketSizeCeiling, the hard packet-size ceiling applied after the ChromeParrot override.
replace github.com/sagernet/quic-go => github.com/Piggy-Cat-bit-shadow/quic-go v0.61.1-0.20261019061042-7e08ecaba383

// sing-quic carries that ceiling through qtls.QUICOptions. This module does not import it today,
// so the replace is here to keep the two modules pinned to the same revision: the guard in
// scripts/ci/check-go-module-integrity.sh fails when a fork the root builds drifts here, which is
// how the test module was once found resolving a different sing-tun than the product ships.
replace github.com/sagernet/sing-quic => github.com/Piggy-Cat-bit-shadow/sing-quic v0.7.2-0.20261019060901-d0deb0359c06

// The cronet-go pin is a tree of modules, not one: see the root module for why every path the
// repository publishes is pinned. A nested module does not inherit the root's replaces.
replace github.com/sagernet/cronet-go => github.com/Piggy-Cat-bit-shadow/cronet-go v0.0.1-143.0.7499.109-2.0.20261020055023-fd2f25825e4f

replace github.com/sagernet/cronet-go/all => github.com/Piggy-Cat-bit-shadow/cronet-go/all v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/android_386 => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/android_386 v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/android_amd64 => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/android_amd64 v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/android_arm => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/android_arm v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/android_arm64 => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/android_arm64 v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/darwin_amd64 => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/darwin_amd64 v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/darwin_arm64 => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/darwin_arm64 v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/ios_amd64_simulator => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/ios_amd64_simulator v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/ios_arm64 => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/ios_arm64 v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/ios_arm64_simulator => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/ios_arm64_simulator v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_386 => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_386 v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_386_musl => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_386_musl v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_amd64 => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_amd64 v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_amd64_musl => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_amd64_musl v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_arm => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_arm v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_arm64 => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_arm64 v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_arm64_musl => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_arm64_musl v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_arm_musl => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_arm_musl v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_loong64 => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_loong64 v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_loong64_musl => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_loong64_musl v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_mips64le => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_mips64le v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_mipsle => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_mipsle v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_mipsle_musl => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_mipsle_musl v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_riscv64 => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_riscv64 v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/linux_riscv64_musl => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/linux_riscv64_musl v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/tvos_amd64_simulator => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/tvos_amd64_simulator v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/tvos_arm64 => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/tvos_arm64 v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/tvos_arm64_simulator => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/tvos_arm64_simulator v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/windows_amd64 => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/windows_amd64 v0.0.0-20260929202119-8c68ce89873c

replace github.com/sagernet/cronet-go/lib/windows_arm64 => github.com/Piggy-Cat-bit-shadow/cronet-go/lib/windows_arm64 v0.0.0-20260929202119-8c68ce89873c

replace github.com/metacubex/utls => github.com/Piggy-Cat-bit-shadow/utls v1.8.8-0.20261009084700-6c3e08ed4c4d
