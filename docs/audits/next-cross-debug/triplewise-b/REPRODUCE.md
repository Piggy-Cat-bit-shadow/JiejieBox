# Reproduce — triple-wise B (the complete lookup's cross-epoch refusal)

Windows PowerShell.

## 0. Environment

```powershell
$env:PATH = 'C:\Deepseek\.tools\go1.27.4\go\bin;' + $env:PATH
$env:GOFLAGS = '-mod=mod'
cd C:\Deepseek\JiejieBox
```

## 1. The guard and its control

```powershell
go test -count=1 -run 'TestACompleteLookupSplitAcrossAnEpochChangeIsRefused|TestACompleteLookupOnOneEpochReturnsBothFamilies' -v ./dns/
# --- PASS: TestACompleteLookupSplitAcrossAnEpochChangeIsRefused (0.00s)
# --- PASS: TestACompleteLookupOnOneEpochReturnsBothFamilies (0.00s)
go test -count=1 ./dns/     # ok 8.63 s
```

What the guard forces, step by step (`dns/client_complete_epoch_test.go`):

1. `familyGateTransport` answers A immediately and parks AAAA on a channel.
2. The complete lookup runs in a goroutine; the test waits for the A half.
3. The test advances the generation (`NetworkGeneration` returns it) and releases AAAA.
4. The lookup must return the `address families belong to different networks` error and a nil set.
5. The control repeats 1-2 without step 3 and requires `[192.0.2.1 2001:db8::1]`.

## 2. Mutation control

In `dns/client.go`, `lookupComplete`, disable the comparison:

```go
if lookupHasEpoch && false && c.networkGeneration() != lookupEpoch {   // MUTATION
```

```powershell
go test -count=1 -run 'TestACompleteLookupSplitAcrossAnEpochChangeIsRefused' ./dns/
# --- FAIL: An error is expected but got nil
```

Restore the line afterwards. The failure is the proof that the guard - not the harness - is what makes
the test pass.

## 3. Package regressions executed for this window

```powershell
$tags = (Get-Content release/DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()
go test -count=1 -timeout 1200s -tags $tags ./provider/... ./common/dialer/ ./protocol/group/ ./common/urltest/
# ok provider/local 0.854s / provider/parser 0.162s / provider/remote 0.625s
# ok common/dialer 21.980s / protocol/group 7.301s / common/urltest 8.405s
```

## 4. Why the joint TB-4 chain is not in this list

`dns` imports `common/dialer` (`dns/transport_dialer.go`), so a test in `common/dialer` cannot import
`dns`. The joint harness has to live in `dns` or `route`: build a real `Client` over a scripted
transport, pass it to `dialer.New` as the router, warm the cache under generation 1, advance the
generation, and assert the candidate list a dial receives contains no address stamped before the
advance. The `dns` package already exposes the source stamp and the generation option
(`ClientOptions.NetworkGeneration`), so no new hooks are needed - only the wiring, which is the next
step recorded in `MATRIX.md` §2.
