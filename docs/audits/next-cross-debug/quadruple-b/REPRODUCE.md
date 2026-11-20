# Reproduce — quadruple B (Q05: cache × dialer × epoch)

Windows PowerShell.

## 0. Environment

```powershell
$env:PATH = 'C:\Deepseek\.tools\go1.27.4\go\bin;C:\Deepseek\.tools\mingw64\bin;' + $env:PATH
$env:CGO_ENABLED = '1'      # for -race
$env:GOFLAGS = '-mod=mod'
cd C:\Deepseek\JiejieBox
```

## 1. The joint chain

```powershell
go test -count=1 -run 'TestTheRealCacheFeedsTheRealDialerAcrossANetworkChange' -v ./dns/
# --- PASS
go test -count=1 ./dns/          # ok 8.63 s
go test -count=1 -race ./dns/    # ok 9.91 s
```

What the test does, step by step (`dns/client_dial_candidate_test.go`):

1. two TCP listeners on **one** port: `127.0.0.1:P` and `127.0.0.2:P`;
2. the production `dns.Client` over a scripted transport whose A answer is `127.0.0.1` and which counts
   A queries;
3. the production `dialer.NewWithOptions(..., RemoteIsDomain: true)`, with an `adapter.DNSRouter` that
   forwards into `Client.Lookup` and no detour;
4. an `adapter.NetworkManager` fake whose `NetworkEnvironment()` is the generation;
5. arm 1: dial twice with no change - the second dial must add **no A query** and land on the same
   listener;
6. arm 2: set the answer to `127.0.0.2`, advance the generation, call
   `Client.refreshTransportEnvironments()` (what `Router.ResetNetwork` does), dial - it must land on
   the **second** listener with a fresh A query.

## 2. The mutation control

Comment out the pin refresh in the test:

```go
// dnsClient.refreshTransportEnvironments()
```

```powershell
go test -count=1 -run 'TestTheRealCacheFeedsTheRealDialerAcrossANetworkChange' ./dns/
# --- FAIL
# the dial after the network change landed on the old address: a cached answer crossed the
# generation boundary
```

Restore the line afterwards. This is a *test-side* mutation: it models a transition that forgets to
move the cache namespace, and the assertion names that exact failure.

## 3. The in-flight half (already covered)

```powershell
go test -count=1 -run 'TestExchangeReturnsAnAnswerFromTheNetworkItWasAskedOn|TestExchangeAsyncDoesNotDeliverASupersededAnswerAsSuccess' -v ./dns/
```

That file (`cross_generation_answer_test.go`, B1/B3) covers what a caller receives when the network
changes *while its query is in flight*; this window's test covers the sequential chain, and the two
are deliberately different questions.

## 4. Not reproducible here

- **Q01**: the `auto_redirect`/nftables packet path needs a Linux netns with root. The fake-IP store's
  guards were inherited from earlier rounds.
- **Q04**: the provider/selector/physical-socket harness was not built; the design is in `MATRIX.md`
  §3 of `quadruple-b`.
- **Q06**: a live TUN flow needs a TUN device.
