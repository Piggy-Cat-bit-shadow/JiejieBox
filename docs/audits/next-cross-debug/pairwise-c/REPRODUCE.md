# Reproduce — window C (autonomous exploration)

Exact commands, in the order they were run. Windows PowerShell.

## 0. Environment

```powershell
$env:PATH = 'C:\Deepseek\.tools\go1.27.4\go\bin;' + $env:PATH
$env:PATH = 'C:\Deepseek\.tools\mingw64\bin;' + $env:PATH; $env:CGO_ENABLED = '1'   # for -race
$env:GOFLAGS = '-mod=mod'
cd C:\Deepseek\JiejieBox
```

## 1. `C-P01` — the certificate reload must not publish an older snapshot last

### 1.1 Green (current HEAD)

```powershell
go test -count=1 -run 'TestOverlappingReloadsCannotPublishAStaleTrustSet' -v -timeout 120s ./common/certificate/
# --- PASS (about 2 s: the test waits for the second reload, which the fix serialises)
go test -count=1 ./common/certificate/                 # ok 2.08 s
go test -race -count=2 ./common/certificate/           # ok 5.24 s
```

The test builds two CAs, writes the first to a `certificate_path`, constructs the store through
`NewStore` (the real entry), then:

1. holds reload A between its file read and its publish, through the package's `testHookStoreLoad`;
2. rewrites the file with CA-2 and starts reload B (a second watcher callback for the same change);
3. releases A;
4. requires `store.Pool()` to verify a leaf signed by **CA-2** - the file's current content.

### 1.2 Red (the mutation)

Remove the two lines at the top of `Store.update()` in `common/certificate/store.go`:

```go
	s.updateAccess.Lock()
	defer s.updateAccess.Unlock()
```

```powershell
go test -count=1 -run 'TestOverlappingReloadsCannotPublishAStaleTrustSet' -timeout 120s ./common/certificate/
# --- FAIL: the store published the trust set read BEFORE the file changed over the one read after it
```

That is the mutation control: the same test, the same hook, one line removed. Restore the file
before committing.

**Why the hook is not `sync.Once`:** `Once.Do` blocks every other caller until the first body
returns, which would serialise the two reloads inside the test and hide the defect. The test uses
`atomic.Bool.CompareAndSwap`, so only the first reload is held and the second runs freely - which is
what a second watcher callback does.

## 2. `C-P02` — the reload does not reach the TLS clients (reproduction for the next round)

No production change was made; this is the executable experiment that would settle it:

1. Write a CA file and construct a `certificate.Store` from it (`NewStore`, `CertificatePath`).
2. Put the store in a service context (`service.ContextWith[adapter.CertificateStore]`) and build a
   TLS client config with `common/tls.NewClient` - this captures `RootCAs = store.Pool()`.
3. Start a local TLS server whose leaf is signed by a **second** CA that is not in the file yet.
4. Rewrite the CA file with the second CA and wait until `store.Pool()` is a new pointer (the watcher
   reloaded). A config built **now** verifies the server.
5. Handshake through the config from step 2, through `common/tls.ClientHandshake` (what
   `defaultDialer.dialContext` calls): it fails with `x509: certificate signed by unknown authority`.
6. Positive control: the Windows engine built from the same store and options (`Engine: windows`)
   verifies the server after the reload, because it reads `c.store.Pool()` at verification time
   (`common/tls/windows_client.go:296-302`).

Step 5 today is the defect; step 6 is why it is an inconsistency rather than an upstream limitation.

## 3. `C-P06` — the packages no workflow runs

```powershell
go test -count=1 ./common/netcap/ ./common/physicalpath/ ./common/power/ ./common/schannel/ ./common/suspendwatch/
# all ok (0.069 s / 0.090 s / 4.772 s / 0.076 s / 0.131 s)

go test -count=1 -v ./common/tlsspoof/
# 6 pure-logic tests PASS (record framing, size 303 B, one segment, ALPN, SNI, no PQ key share)
# 3 WinDivert integration tests FAIL: tls_spoof: open WinDivert: windivert: open SCM: Access is denied.

go test -count=1 ./common/windivert/
# 5 integration tests FAIL: windivert: open SCM: Access is denied.
```

Run them from an elevated shell to execute the WinDivert half; this session had no Administrator
token, so those are `NOT_OBSERVABLE` here and in CI.

## 4. What was not attacked

`X-P02` (SOCKS UDP association), `X-P04` (detour chain × cancel), `X-P06` (rule-provider failure),
`X-P08` (DNS multiplexing) are `NOT_COVERED`: the packages already carry dense suites (listed in
`MATRIX.md`) and no new failure mode was constructed this round. `X-P03` (mieru underlay) and `X-P05`
(netns) are recorded as `SOURCE_ONLY` and `NOT_OBSERVABLE` respectively.
