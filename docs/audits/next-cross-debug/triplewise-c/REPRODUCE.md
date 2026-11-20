# Reproduce — triple-wise C (the server TLS reload race)

Windows PowerShell, `-race` needs the mingw64 gcc.

## 0. Environment

```powershell
$env:PATH = 'C:\Deepseek\.tools\go1.27.4\go\bin;C:\Deepseek\.tools\mingw64\bin;' + $env:PATH
$env:CGO_ENABLED = '1'
$env:GOFLAGS = '-mod=mod'
cd C:\Deepseek\JiejieBox
```

## 1. Green (current HEAD)

```powershell
go test -count=1 -race -run 'TestAReplacementIsVisibleToEveryReaderWithoutARace|TestConcurrentCertificateAndKeyCallbacksAreSerialised' -v ./common/tls/
# --- PASS: TestAReplacementIsVisibleToEveryReaderWithoutARace
# --- PASS: TestConcurrentCertificateAndKeyCallbacksAreSerialised
go test -count=1 -race ./common/tls/                  # ok 4.98 s
go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./common/tls/   # ok 3.64 s
```

What the two tests force:

1. **Readers against the swap** - four goroutines call `Server(nil)`, `Client(nil)`, `STDConfig()`,
   `ServerName()` and `NextProtos()` in a loop while the test rewrites the pair and calls
   `certificateUpdated` for the certificate path and then the key path. This also asserts the
   half-renewal invariant: while the new certificate is in memory but the key is still the old one, the
   reload reports `private key does not match public key` and `STDConfig()` still returns the OLD pair.
2. **Two callbacks at once** - 50 rounds of `certificateUpdated(cert)` and `certificateUpdated(key)`
   in parallel goroutines, then the served DER must equal the pair on disk.

## 2. Red (the mutation)

In `common/tls/std_server.go`, delete the two lines at the top of `certificateUpdated`:

```go
	c.reloadAccess.Lock()
	defer c.reloadAccess.Unlock()
```

```powershell
go test -count=1 -race -run 'TestConcurrentCertificateAndKeyCallbacksAreSerialised' ./common/tls/
# WARNING: DATA RACE
#   common/tls/std_server.go:299  (c.certificate = certificate / c.key = key)
#   common/tls/std_server.go:305  (tls.X509KeyPair(c.certificate, c.key))
# --- FAIL
```

That is the mutation control: the same test, the same files, one lock removed.

## 3. The original state (before the fix)

Reverting the whole `89fef8ca0` change reproduces the same reports **without** the mutation, because
the reader side (`Server`, `Client`, `STDConfig`, `Clone`) also read `c.config` unlocked - the second
race. `git revert --no-commit 89fef8ca0` in a scratch worktree is the clean way to see it; do not
revert on the working branch.

## 4. The family scan (how the target was chosen)

```powershell
Get-ChildItem -Recurse -Include *.go | Select-String -Pattern 'sagernet/fswatch|fswatch.NewWatcher'
# common/certificate/store.go          -> fixed earlier this session (6eb1f1a8b)
# common/tls/std_server.go             -> fixed here (89fef8ca0)
# route/rule/rule_set_local.go         -> already access-guarded (NO_DEFECT)
# route/neighbor_resolver_linux.go     -> RWMutex on every map access (NO_DEFECT)
# route/neighbor_resolver_darwin.go    -> same shape (NO_DEFECT)
# provider/local/lcoal.go              -> delegates to the provider adapter (NOT_COVERED)
```

## 5. Not reproducible here

The neighbour-resolver reload needs Linux netlink or Darwin, and the ECH-key branch needs a live ECH
configuration; both are recorded as not covered rather than claimed.
