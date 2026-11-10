package dialer

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Streaming launch order (§A).
//
// # The defect these pin
//
// A static plan is interleaved before it reaches the scheduler, so the first candidate of each
// family is attempted early. A candidate that arrives through the late stream never passed through
// the planner, and the scheduler appended it to ONE flat queue. A family that answered first with
// eight addresses therefore queued all eight ahead of the other family:
//
//	AAAA#1 starts, AAAA#2..#8 queue, healthy A arrives, A queues at position 9
//	A starts ~8 fallback intervals later
//
// At the default 300ms delay that is roughly 2.4s between the first dial and the first attempt of a
// family that is demonstrably healthy. The tests below fix the ORDER as the observable, not the wall
// clock alone: a fast-failing blackhole that advances the schedule cannot be used to make a wrong
// order look fast.

// streamOrderDialer is the scheduler's executor in these tests. It silently blackholes the named
// addresses - never answering and never failing on its own until the race cancels it - and echoes
// data on every other address, so "the connection works" is measured with real payload bytes.
//
// A silent blackhole is deliberate: a blackhole that reports its own path timeout advances the
// launch schedule by itself, which is exactly the mechanism that can hide an ordering defect. Here
// only the fallback timer can start a candidate, so the ORDER is a property of the scheduler.
type streamOrderDialer struct {
	access sync.Mutex
	stall  map[netip.Addr]bool
	order  []netip.Addr
	times  []time.Time
}

func newStreamOrderDialer(stalled ...netip.Addr) *streamOrderDialer {
	stall := make(map[netip.Addr]bool, len(stalled))
	for _, address := range stalled {
		stall[address] = true
	}
	return &streamOrderDialer{stall: stall}
}

func (d *streamOrderDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.access.Lock()
	d.order = append(d.order, destination.Addr)
	d.times = append(d.times, time.Now())
	stalled := d.stall[destination.Addr]
	d.access.Unlock()

	if stalled {
		// A path that accepts packets and never answers. Nothing is reported until the race is
		// cancelled, so no failure can advance the schedule.
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return echoConnection(), nil
}

func (d *streamOrderDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

func (d *streamOrderDialer) attempt(ctx context.Context, address netip.Addr) (net.Conn, error) {
	return d.DialContext(ctx, "tcp", M.SocksaddrFrom(address, 443))
}

func (d *streamOrderDialer) attempts() []netip.Addr {
	d.access.Lock()
	defer d.access.Unlock()
	return append([]netip.Addr(nil), d.order...)
}

func (d *streamOrderDialer) startedAt(address netip.Addr) (time.Time, bool) {
	d.access.Lock()
	defer d.access.Unlock()
	for index, tried := range d.order {
		if tried == address {
			return d.times[index], true
		}
	}
	return time.Time{}, false
}

// attemptsBefore counts how many OTHER addresses were attempted before this one. It is the
// load-independent statement of the defect: before the fix the healthy family was last, so this was
// the size of the whole blackholed batch.
func (d *streamOrderDialer) attemptsBefore(address netip.Addr) int {
	d.access.Lock()
	defer d.access.Unlock()
	for index, tried := range d.order {
		if tried == address {
			return index
		}
	}
	return -1
}

// echoConnection returns one end of an in-memory pipe with an echo server on the other end, so a
// test can prove the winning connection carries data rather than merely being non-nil.
func echoConnection() net.Conn {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		buffer := make([]byte, 128)
		for {
			read, err := server.Read(buffer)
			if err != nil {
				return
			}
			if _, err = server.Write(buffer[:read]); err != nil {
				return
			}
		}
	}()
	return client
}

// requireBusinessData writes a payload through the winning connection and reads the echo back.
func requireBusinessData(t *testing.T, conn net.Conn) {
	t.Helper()
	payload := []byte("business-data")
	require.NoError(t, conn.SetDeadline(time.Now().Add(2*time.Second)))
	_, err := conn.Write(payload)
	require.NoError(t, err)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err, "the winning connection must carry data end to end")
	require.Equal(t, payload, echo)
}

func batchAddresses(prefix string, first, count int, ipv6 bool) []netip.Addr {
	addresses := make([]netip.Addr, 0, count)
	for index := 0; index < count; index++ {
		var text string
		if ipv6 {
			text = prefix + "::" + itoa(first+index)
		} else {
			text = prefix + "." + itoa(first+index)
		}
		addresses = append(addresses, netip.MustParseAddr(text))
	}
	return addresses
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [8]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}

// streamBatch feeds one family's whole batch, then waits for the scheduler to have started the
// first one, and only then offers the other family. The barrier makes "the first family's batch had
// already entered the scheduler" true by construction rather than by a sleep.
func streamBatch(
	addresses []netip.Addr,
	family addressFamily,
	other []netip.Addr,
	otherFamily addressFamily,
	firstStarted <-chan struct{},
) chan dualStackCandidate {
	candidates := make(chan dualStackCandidate, len(addresses)+len(other))
	go func() {
		defer close(candidates)
		for _, address := range addresses {
			candidates <- dualStackCandidate{address: address, family: family}
		}
		<-firstStarted
		// The other family answers while the first batch is being worked through.
		time.Sleep(5 * time.Millisecond)
		for _, address := range other {
			candidates <- dualStackCandidate{address: address, family: otherFamily}
		}
	}()
	return candidates
}

// TestStreamOrderDoesNotStarveTheOtherFamily is the gate for the streaming half of A.
//
// Eight silently blackholed addresses of one family, then one healthy address of the other. The
// healthy address must be attempted after at most the one candidate that was already in flight -
// not after the whole batch - and the same must hold under both preference directions, because a
// preference is an ordering hint and must never become an exclusion.
func TestStreamOrderDoesNotStarveTheOtherFamily(t *testing.T) {
	const fallbackDelay = 40 * time.Millisecond

	blocked := batchAddresses("2001:db8", 1, 8, true)
	healthy := netip.MustParseAddr("192.0.2.1")

	for _, testCase := range []struct {
		name     string
		strategy C.DomainStrategy
	}{
		{"prefer_ipv6", C.DomainStrategyPreferIPv6},
		{"prefer_ipv4", C.DomainStrategyPreferIPv4},
		{"asis", C.DomainStrategyAsIS},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			inner := newStreamOrderDialer(blocked...)
			firstStarted := make(chan struct{})
			var once sync.Once
			attempt := func(attemptCtx context.Context, address netip.Addr) (net.Conn, error) {
				once.Do(func() { close(firstStarted) })
				return inner.attempt(attemptCtx, address)
			}

			late := streamBatch(blocked, familyIPv6, []netip.Addr{healthy}, familyIPv4, firstStarted)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			scheduler := &candidateScheduler{fallbackDelay: fallbackDelay}
			conn, winner, err := scheduler.dialWithLateCandidates(ctx,
				planCandidates(nil, netip.Addr{}, testCase.strategy), late, attempt)
			require.NoError(t, err)
			require.Equal(t, healthy, winner, "the healthy family must win the race it joins")
			requireBusinessData(t, conn)
			require.NoError(t, conn.Close())

			before := inner.attemptsBefore(healthy)
			require.GreaterOrEqual(t, before, 0, "the healthy candidate must have been attempted")
			require.LessOrEqual(t, before, 2,
				"the healthy family was attempted after %d candidates of the blackholed batch; "+
					"a streaming batch must be interleaved like a static plan, not appended behind it "+
					"(attempts: %v)", before, inner.attempts())

			// The decisive latency statement, expressed in cadences so it is not a host measurement:
			// the healthy family starts within a small constant number of fallback intervals after
			// the race begins, not after the size of the other family's batch.
			blockedAt, blockedStarted := inner.startedAt(blocked[0])
			healthyAt, healthyStarted := inner.startedAt(healthy)
			require.True(t, blockedStarted)
			require.True(t, healthyStarted)
			require.Less(t, healthyAt.Sub(blockedAt), 3*fallbackDelay,
				"the healthy family started %v after the race began; with eight queued candidates "+
					"a flat queue puts it at 8 fallback intervals", healthyAt.Sub(blockedAt))

			// Within-family resolver order is still authoritative: only the resolver's prefix was
			// attempted, in the order the resolver returned it.
			attempts := inner.attempts()
			for index, address := range attempts {
				if address == healthy {
					break
				}
				require.Equal(t, blocked[index], address,
					"a family's own candidates must keep their resolver order")
			}
		})
	}
}

// TestStreamOrderMirrorFamilyDoesNotStarve is the mirror direction.
func TestStreamOrderMirrorFamilyDoesNotStarve(t *testing.T) {
	const fallbackDelay = 40 * time.Millisecond

	blocked := batchAddresses("192.0.2", 1, 8, false)
	healthy := netip.MustParseAddr("2001:db8::1")

	inner := newStreamOrderDialer(blocked...)
	firstStarted := make(chan struct{})
	var once sync.Once
	attempt := func(attemptCtx context.Context, address netip.Addr) (net.Conn, error) {
		once.Do(func() { close(firstStarted) })
		return inner.attempt(attemptCtx, address)
	}

	late := streamBatch(blocked, familyIPv4, []netip.Addr{healthy}, familyIPv6, firstStarted)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scheduler := &candidateScheduler{fallbackDelay: fallbackDelay}
	conn, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv6), late, attempt)
	require.NoError(t, err)
	require.Equal(t, healthy, winner)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())

	require.LessOrEqual(t, inner.attemptsBefore(healthy), 2,
		"the late IPv6 family must not queue behind the whole IPv4 batch; attempts: %v", inner.attempts())
}

// TestStreamOrderInterleavesFullyStreamedDualStackAnswer pins the ordering rule directly.
//
// Both families arrive late as family blocks, which is what a real resolver stream does. The launch
// order must be the interleaving planCandidates would have produced - v6#1 v4#1 v6#2 v4#2 v6#3 -
// rather than two family blocks one after the other.
func TestStreamOrderInterleavesFullyStreamedDualStackAnswer(t *testing.T) {
	const fallbackDelay = 50 * time.Millisecond

	v6 := batchAddresses("2001:db8", 1, 3, true)
	v4 := batchAddresses("192.0.2", 1, 2, false)

	// Every candidate but the last v6 address stalls, so the timer is the only thing that can
	// advance the schedule and the race ends on the last interleaved launch.
	inner := newStreamOrderDialer(v6[0], v6[1], v4[0], v4[1])

	late := make(chan dualStackCandidate, len(v6)+len(v4))
	for _, address := range v6 {
		late <- dualStackCandidate{address: address, family: familyIPv6}
	}
	for _, address := range v4 {
		late <- dualStackCandidate{address: address, family: familyIPv4}
	}
	close(late)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scheduler := &candidateScheduler{fallbackDelay: fallbackDelay}
	conn, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv6), late, inner.attempt)
	require.NoError(t, err)
	require.Equal(t, v6[2], winner)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())

	require.Equal(t, []netip.Addr{v6[0], v4[0], v6[1], v4[1], v6[2]}, inner.attempts(),
		"a fully streamed dual-stack answer must interleave exactly like a static plan")
}

// TestStreamOrderReachesTheOtherFamilyAfterTheTimerFired is the "timer already expired" contrast.
//
// The blackholed family has already consumed a launch slot from the timer when the healthy family
// answers. The healthiest it can be is the NEXT launch, not the one after the remaining batch.
func TestStreamOrderReachesTheOtherFamilyAfterTheTimerFired(t *testing.T) {
	const fallbackDelay = 40 * time.Millisecond

	blocked := batchAddresses("2001:db8", 1, 8, true)
	healthy := netip.MustParseAddr("192.0.2.1")

	inner := newStreamOrderDialer(blocked...)
	late := make(chan dualStackCandidate, len(blocked)+1)
	for _, address := range blocked {
		late <- dualStackCandidate{address: address, family: familyIPv6}
	}

	// Wait until the timer has already started the second blocked candidate, so the healthy family
	// genuinely arrives after a cadence boundary.
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if len(inner.attempts()) >= 2 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		time.Sleep(5 * time.Millisecond)
		late <- dualStackCandidate{address: healthy, family: familyIPv4}
		close(late)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scheduler := &candidateScheduler{fallbackDelay: fallbackDelay}
	conn, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv6), late, inner.attempt)
	require.NoError(t, err)
	require.Equal(t, healthy, winner)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())

	before := inner.attemptsBefore(healthy)
	require.GreaterOrEqual(t, before, 2, "the race must already have started two blocked candidates")
	require.LessOrEqual(t, before, 3,
		"after the cadence has fired, the healthy family must still be the next launch rather than "+
			"the tail of the batch; attempts: %v", inner.attempts())
}

// TestStreamOrderHealthyFamilyFirstIsNotDelayed is the out-of-order arrival contrast.
//
// The healthy family answers first and the blackholed batch arrives behind it. The healthy attempt
// must keep the launch slot it already had - an ordering fix must not introduce a wait for a family
// that has not answered.
func TestStreamOrderHealthyFamilyFirstIsNotDelayed(t *testing.T) {
	const fallbackDelay = 40 * time.Millisecond

	blocked := batchAddresses("2001:db8", 1, 8, true)
	healthy := netip.MustParseAddr("192.0.2.1")

	inner := newStreamOrderDialer(blocked...)
	firstStarted := make(chan struct{})
	var once sync.Once
	attempt := func(attemptCtx context.Context, address netip.Addr) (net.Conn, error) {
		once.Do(func() { close(firstStarted) })
		return inner.attempt(attemptCtx, address)
	}

	late := streamBatch([]netip.Addr{healthy}, familyIPv4, blocked, familyIPv6, firstStarted)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	scheduler := &candidateScheduler{fallbackDelay: fallbackDelay}
	conn, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv6), late, attempt)
	require.NoError(t, err)
	require.Equal(t, healthy, winner)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())

	require.Equal(t, 0, inner.attemptsBefore(healthy),
		"the healthy family answered first and must take the first launch slot")
	require.Less(t, time.Since(start), 3*fallbackDelay,
		"the healthy family must not be delayed by a family that answered later")
}

// TestStreamOrderBothFamiliesAnswerLate is the "both DNS answers are slow" contrast.
//
// Nothing answers for a while, so no cadence was armed before either family existed. The first
// candidate to arrive must still start immediately, and the second family must then be reached on
// the normal cadence rather than being deferred by its absence from the empty plan.
func TestStreamOrderBothFamiliesAnswerLate(t *testing.T) {
	const fallbackDelay = 50 * time.Millisecond

	stalled6 := netip.MustParseAddr("2001:db8::1")
	healthy4 := netip.MustParseAddr("192.0.2.1")

	inner := newStreamOrderDialer(stalled6)
	late := make(chan dualStackCandidate, 2)
	released := make(chan time.Time, 1)
	go func() {
		time.Sleep(60 * time.Millisecond)
		released <- time.Now()
		late <- dualStackCandidate{address: stalled6, family: familyIPv6}
		time.Sleep(2 * time.Millisecond)
		late <- dualStackCandidate{address: healthy4, family: familyIPv4}
		close(late)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scheduler := &candidateScheduler{fallbackDelay: fallbackDelay}
	conn, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv6), late, inner.attempt)
	require.NoError(t, err)
	require.Equal(t, healthy4, winner)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())

	firstAt, started := inner.startedAt(stalled6)
	require.True(t, started)
	require.Less(t, firstAt.Sub(<-released), 25*time.Millisecond,
		"the first candidate of a slow answer must start immediately, not wait for a cadence that "+
			"was scheduled before it existed")
	require.LessOrEqual(t, inner.attemptsBefore(healthy4), 1,
		"the second family must be the next launch, not appended behind the first family's batch")
}

// TestStreamOrderStrictStrategyStillDropsTheForbiddenFamily guards the interaction between the new
// ordering and the operator's policy: interleaving must not admit a family the strategy forbids.
func TestStreamOrderStrictStrategyStillDropsTheForbiddenFamily(t *testing.T) {
	forbidden := batchAddresses("2001:db8", 1, 2, true)
	healthy := netip.MustParseAddr("192.0.2.1")

	inner := newStreamOrderDialer(forbidden...)
	late := make(chan dualStackCandidate, len(forbidden)+1)
	for _, address := range forbidden {
		late <- dualStackCandidate{address: address, family: familyIPv6}
	}
	late <- dualStackCandidate{address: healthy, family: familyIPv4}
	close(late)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	admitIPv4 := func(address netip.Addr) bool { return classifyAddress(address) == familyIPv4 }
	scheduler := &candidateScheduler{fallbackDelay: 40 * time.Millisecond}
	conn, winner, err := scheduler.dialWithLateCandidatesAdmitting(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyIPv4Only), late, admitIPv4, inner.attempt)
	require.NoError(t, err)
	require.Equal(t, healthy, winner)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())

	require.Equal(t, []netip.Addr{healthy}, inner.attempts(),
		"the forbidden family must not be attempted at all, whatever the launch order")
}

// streamRace runs one streaming race against a fresh executor.
func streamRace(
	t *testing.T,
	health *familyHealth,
	generation uint64,
	fallbackDelay time.Duration,
	timeout time.Duration,
	stalled netip.Addr,
	healthy netip.Addr,
) (*streamOrderDialer, netip.Addr, error) {
	t.Helper()

	inner := newStreamOrderDialer(stalled)
	late := make(chan dualStackCandidate, 2)
	go func() {
		late <- dualStackCandidate{address: stalled, family: familyIPv6}
		time.Sleep(2 * time.Millisecond)
		late <- dualStackCandidate{address: healthy, family: familyIPv4}
		close(late)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	scheduler := &candidateScheduler{
		fallbackDelay:     fallbackDelay,
		health:            health,
		networkGeneration: generation,
	}
	_, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv6), late, inner.attempt)
	return inner, winner, err
}

// TestStreamOrderConsumesTheFamilyVerdictAtArrivalTime is the second-request half of A.
//
// Connection one measures that IPv6 stalls while IPv4 connects. Connection two must then start
// IPv4's first candidate TOGETHER with IPv6's, instead of making it wait out the fallback delay -
// exactly what a static plan gets from the same verdict.
//
// The second race uses a fallback delay far beyond the test's deadline, so the only way IPv4 can be
// attempted at all is by consuming the verdict. Expiry, generation change and recovery each put the
// cadence back, which is what keeps the nudge a bounded optimisation rather than a permanent demotion.
func TestStreamOrderConsumesTheFamilyVerdictAtArrivalTime(t *testing.T) {
	const (
		observedGeneration = 7
		otherGeneration    = 8
	)
	stalled := netip.MustParseAddr("2001:db8::1")
	healthy := netip.MustParseAddr("192.0.2.1")

	t.Run("verdict starts the other family immediately", func(t *testing.T) {
		health := newFamilyHealth()
		// Race one, with a reachable cadence, observes the verdict.
		_, firstWinner, err := streamRace(t, health, observedGeneration, 20*time.Millisecond,
			2*time.Second, stalled, healthy)
		require.NoError(t, err)
		require.Equal(t, healthy, firstWinner)
		require.True(t, health.fallbackImmediately(observedGeneration, familyIPv6, familyIPv4),
			"the first race must have recorded IPv6 as stalled while IPv4 connected")

		// Race two: the cadence cannot fire inside the window.
		start := time.Now()
		inner, winner, err := streamRace(t, health, observedGeneration, 2*time.Second,
			500*time.Millisecond, stalled, healthy)
		require.NoError(t, err,
			"with the verdict in force IPv4 must start immediately; without consuming it, "+
				"it waits out the 2s cadence and the call times out")
		require.Equal(t, healthy, winner)
		require.Less(t, time.Since(start), 400*time.Millisecond)
		require.LessOrEqual(t, inner.attemptsBefore(healthy), 1,
			"IPv4 must start as the other family's first candidate, not behind a burst")
	})

	for _, testCase := range []struct {
		name   string
		adjust func(health *familyHealth)
		// generation for the second race.
		generation uint64
	}{
		{
			name:       "generation change",
			adjust:     func(health *familyHealth) {},
			generation: otherGeneration,
		},
		{
			name: "expired verdict",
			adjust: func(health *familyHealth) {
				health.access.Lock()
				entry := health.state[observedGeneration]
				entry.failedAt = time.Now().Add(-2 * tcpTimeoutWindow)
				health.state[observedGeneration] = entry
				health.access.Unlock()
			},
			generation: observedGeneration,
		},
		{
			name: "family recovered",
			adjust: func(health *familyHealth) {
				health.recordSuccess(observedGeneration, familyIPv6)
			},
			generation: observedGeneration,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			health := newFamilyHealth()
			_, firstWinner, err := streamRace(t, health, observedGeneration, 20*time.Millisecond,
				2*time.Second, stalled, healthy)
			require.NoError(t, err)
			require.Equal(t, healthy, firstWinner)
			testCase.adjust(health)

			inner, _, err := streamRace(t, health, testCase.generation, 2*time.Second,
				250*time.Millisecond, stalled, healthy)
			require.Error(t, err,
				"the verdict must no longer accelerate the other family, so the out-of-reach "+
					"cadence leaves the call to its deadline")
			require.NotContains(t, inner.attempts(), healthy,
				"the other family must be waiting out the cadence again")
		})
	}
}

// TestDomainStreamingOrderKeepsTheHealthyFamilyReachable drives the production entry point:
// resolveDialer -> DNS family stream -> candidate scheduler -> executor.
//
// Configuring eight AAAA answers and one A answer reproduces the reported shape: the preferred
// family answers first with a batch that silently blackholes, and the healthy family answers a
// moment later. The healthy family must be dialled within a constant number of attempts, and the
// winning connection must carry data.
func TestDomainStreamingOrderKeepsTheHealthyFamilyReachable(t *testing.T) {
	const fallbackDelay = 40 * time.Millisecond

	blocked := batchAddresses("2001:db8", 1, 8, true)
	healthy := netip.MustParseAddr("192.0.2.1")

	router := &fakeDomainRouter{
		addressesAAAA: blocked,
		delayAAAA:     0,
		addressesA:    []netip.Addr{healthy},
		delayA:        5 * time.Millisecond,
	}
	inner := newStreamOrderDialer(blocked...)
	dialer := &resolveDialer{
		router:        router,
		dialer:        inner,
		parallel:      true,
		fallbackDelay: fallbackDelay,
		queryOptions:  adapter.DNSQueryOptions{Strategy: C.DomainStrategyPreferIPv6},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", M.ParseSocksaddr("example.test:443"))
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.NotNil(t, conn)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())

	before := inner.attemptsBefore(healthy)
	require.GreaterOrEqual(t, before, 0)
	require.LessOrEqual(t, before, 4,
		"the healthy family was dialled after %d attempts of the blackholed batch; the streaming "+
			"order must interleave (attempts: %v)", before, inner.attempts())
	require.Less(t, elapsed, 5*fallbackDelay,
		"the healthy family must be reached within a few cadences, not after the whole batch")
}
