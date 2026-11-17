# Go toolchain ABI gates — the inventory this fork must re-check on every upgrade

**Status:** current for `testing` at `4dc2593fb` with toolchain `go1.27.4`.
**Owner:** whoever moves `toolchain` in `go.mod`.

This file exists because of a measurement, not a principle: the 0.1.52 iOS Network Extension crash
(2026-11-17) was caused by a Go 1.27 layout change in `internal/profilerecord.MemProfileRecord`, and
**every gate in `.github/workflows/verify.yml` passed** on that build. The process compiled, linked,
passed the untagged and tagged builds, passed the tag-policy and linkname gates, and then died inside
`writeHeapProto` on a memory-pressure callback 27 minutes into a session.

The general failure mode is:

```text
a first-party type or signature is declared to MATCH a Go internal one
        ↓
//go:linkname passes a slice/pointer of the mirror to the runtime
        ↓
the toolchain moves a field, changes a field's meaning, or changes a signature
        ↓
nothing fails: the linker resolves by NAME, and Go does not type-check across a linkname
        ↓
the mismatch is whatever the runtime does with the wrong bytes — reading, usually
```

So the rule for this repository is: **every first-party dependency on non-public Go surface must name
the mechanism it depends on and the gate that detects drift.** A dependency with no gate is recorded
below as a known gap; it is not assumed safe.

## 1. The inventory

`GATED` means a test in this repository fails when the pinned toolchain changes the assumption.
`LOUD` means the code cannot silently misbehave: it fails to link, or returns an error at runtime.
`GAP` means drift would be silent — these are the rows that carry residual risk.

### 1.1 Runtime profile collection — `experimental/libbox/internal/oomprofile/`

Reached from `service/oomkiller` → `Recorder.snapshot` → `OOMRecorderOptions.SnapshotCallback` →
`oomprofile.WriteFile`, on the NetworkExtension memory-pressure callback. This is the path that
crashed.

| Symbol pulled | What the mirror assumes | Failure if it drifts | Gate |
|---|---|---|---|
| `runtime.pprof_memProfileInternal` | `profilerecord.MemProfileRecord` = `ObjectSize int64; AllocObjects, FreeObjects int64; Stack []uintptr` (48 bytes, `Stack` at 24), and `InUseBytes() = InUseObjects() * ObjectSize` | misread `Stack` slice header → SIGSEGV; wrong byte totals | **GATED** — `mirror_test.go` (reads `$GOROOT/src/internal/profilerecord`, field by field), `oomprofile_test.go` (`TestMemProfileRecordLayoutMatchesGo1274`, per-sample value/label consistency, differential against `runtime/pprof`) |
| `runtime.pprof_blockProfileInternal`, `runtime.pprof_mutexProfileInternal` | `profilerecord.BlockProfileRecord` = `Count, Cycles int64; Stack []uintptr` | misread stacks | **GATED** by the same source comparison |
| `runtime.pprof_threadCreateInternal` | `profilerecord.StackRecord` = `Stack []uintptr` | misread stacks | **GATED** by the same source comparison |
| `runtime.pprof_goroutineProfileWithLabels` | `StackRecord` + `pprof.labelMap` = one `[]Label` slice, `Label` = two strings | labels lost or garbage | **GATED** — `TestLabelMapStillMatchesTheMirror` compares the shape against `$GOROOT/src/internal/runtime/pprof/label/labelset.go`; `TestGoroutineProfileCarriesLabels` proves a labelled goroutine appears labelled in the written profile |
| `runtime/pprof.runtime_cyclesPerSecond` | `func() int64` | wrong `delay` scaling in block/mutex profiles | **GAP** (signature plausible to detect by hand only) |
| `runtime/pprof.runtime_FrameStartLine`, `runtime_FrameSymbolName`, `runtime_expandFinalInlineFrame` | one pointer/string/slice argument each | symbolisation defects | **GAP** |
| `runtime.pprof_makeProfStack` | `func() []uintptr` | expansion buffer wrong | **GAP** |
| `runtime/pprof.parseProcSelfMaps`, `runtime/pprof.elfBuildID` | Linux-only, behind `tfogo_checklinkname0` | link failure if removed (loud); behaviour change if the signature moves | **GAP** (behind the `-checklinkname=0` contract; see §1.4) |
| `runtime/pprof.mach_vm_region`, `runtime/pprof.proc_regionfilename` | `machVMRegionBasicInfoData` mirrors `vm_region_basic_info_data_64_t`; **unchanged between 1.25.5 and 1.27.4** | mapping table malformed | **GAP** (the struct comes from the macOS SDK, not from Go) |

The six profiles this writer produces are `allocs`, `block`, `goroutine`, `heap`, `mutex`,
`threadcreate` (`oomReportProfiles`), and the same list drives the power report.

**Deliberate difference from the standard library:** `runtime/pprof`'s builder wraps its output in
`gzip.BestSpeed`; this package writes raw protobuf, and the `.pb` artifacts in every archived OOM
report are uncompressed. `TestMirrorAgreesWithStandardLibrary` asserts that the written file is NOT
gzip, so a future "simplification" to `pprof.Lookup(...).WriteTo` cannot change the artifact format by
accident. The reason for keeping the copy is performance on the OOM path, not preference: gzip costs
CPU and a 64 KiB window of heap inside the callback that is running because the process is out of
memory.

### 1.2 Runtime structures walked by hand — `experimental/libbox/internal/runtimeinfo/`

| Mirror | Assumes | Failure if it drifts | Gate |
|---|---|---|---|
| `runtime.allgs` + `runtime.g` (`goroutine_badlinkname.go`) | `stack`/`stackguard0`/`stackguard1`/`_panic`/`_defer`/`m`/`sched`/…/`atomicstatus`/`goid` at hand-computed offsets; `gopc` and `startpc` discovered by probing for function names | the probe does not verify → `collectGoroutines` returns nil → the `goroutines` section silently disappears from `runtimeinfo.json` | **GATED** — `goroutine_badlinkname_test.go` fails when the report is nil, empty, or reports no function names/stack bytes |
| `sync.Pool`, `sync.poolLocal`, `sync.poolChainElt` (`pool_badlinkname.go`) | local/victim pointers, 128-byte `poolLocal`, chain head/tail packing | `collectBufferPools` returns nil → `bufferPools` silently disappears | **GATED** — `pool_badlinkname_test.go` |

Both of these degrade to "no section" rather than to garbage, which is why they are P2 rather than
P0: the diagnostic gets smaller, the process does not read arbitrary memory. The gates turn that
silent loss into a failure.

### 1.3 Crash handler — `experimental/libbox/signal_handler_darwin.go`

Pulls `runtime.getsig`, `runtime.setsig`, `runtime.cgoSigtramp`, `runtime.fwdSig`,
`runtime.handlingSig` to install a signal trampoline that writes a crash report. Two of those
(`fwdSig`, and `allgs` above) are why the shipped tag sets carry `-checklinkname=0`.

- Darwin-only, so a Linux push gate can only check the tag contract, not link it.
- **GAP:** a signature or table-layout change in `runtime.sigtable`/`handlingSig` is not gated. It is
  bounded by the fact that the handler installs itself at startup: a change that breaks it should
  fail on the first iOS launch, not after 27 minutes.

### 1.4 The `-checklinkname=0` contract

`tfogo_checklinkname0` is this repository's record that a build passes `-checklinkname=0`.
`cmd/internal/build_libbox/tag_checklinkname_test.go` computes, from the pinned runtime's own source,
which first-party pulls need the flag, and requires:

- every blocked pull to sit behind the tag (or in a file gated on it);
- `release/DEFAULT_BUILD_TAGS*` to NOT name the tag (a bare `go build`/`go test` cannot pass the flag);
- every shipped mobile variant to carry it (otherwise the crash handler and goroutine report silently
  become stubs).

That gate is about **linkability**. It says nothing about whether the pulled symbol still means what
the caller thinks — which is the gap the oomprofile crash fell through.

### 1.5 Other non-public dependencies (not Go-versioned)

| Surface | Where | Why it is out of scope here |
|---|---|---|
| `net/http.readRequest`, `net/http.parseBasicAuth`, `net/url.(*URL).setPath` | `common/badhttp/` | stdlib, but the signatures are stable and the failure is loud (nil/panic on the first request that takes the path) |
| `crypto/tls` method values + `*tls.Conn` field access | `common/badtls/` | fields are resolved BY NAME through `reflect` and validated for kind; a change returns an error (`invalid Conn.isClient`) instead of misreading — **LOUD** |
| `golang.org/x/sys/unix.recvmsg`, `.sendmsg`, `.getsockopt`, `.ioctlPtr`, `.sysctl` | `common/ktls/`, `common/tlsfragment/`, `protocol/bridge/` | dependency source, pinned by `go.mod`/`replace`; a pin move is a reviewable diff |
| `github.com/metacubex/utls.*`, `sing-tun`, `sing-vmess/vless.tlsRegistry`, `tailscale/version.isAppleTV` | `common/ktls/`, `protocol/bridge/`, `protocol/vless/`, `protocol/tailscale/` | dependency source, pinned; the vless one already has `vision_tripwire_test.go` |
| `os.checkPidfdOnce` | `experimental/libbox/pidfd_android.go` | Android-only; a change fails the Android build |

## 2. The upgrade checklist

Run this when `go.mod`'s `toolchain` moves. It is ordered by the cost of being wrong, not by file
order.

1. **Read the toolchain's own source, not the release notes.** The two facts that caused the crash
   were a renamed field pair and a changed per-object semantic; neither is in a release-note bullet.
   `$GOROOT/src/internal/profilerecord/profilerecord.go` and `runtime/pprof/protomem.go` are the
   documents of record.
2. **`go test ./experimental/libbox/internal/...`** — the mirror gates read the installed toolchain, so
   they fail with the field names in the message.
3. **`go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS),tfogo_checklinkname0" -ldflags=-checklinkname=0 ./experimental/libbox/internal/runtimeinfo/`**
   — the runtime-structure mirrors are only compiled with both tags.
4. **`go test ./cmd/internal/...`** — tag composition and the computed linkname policy.
5. **`go build ./...` and `go test -run '^$' ./...` untagged**, then the same with
   `release/DEFAULT_BUILD_TAGS_OTHERS`. A tag set that forgets `tfogo_checklinkname0` becomes a link
   failure here rather than a stub in a shipped artifact.
6. **Cross-compile the targets that cannot run here** — `linux/arm64`, `darwin/arm64`,
   `android/arm64` — because a Darwin-only file can reference a symbol the Linux gate never
   compiled.
7. **Diff the pulled symbol list.** `git grep -n "go:linkname"` and check every target that is not in
   §1.5 against the new toolchain's source. Adding a row to §1.1–§1.3 is the cost of adding a pull.
8. **Then, and only then, run the artifact workflows.** A green `verify.yml` is necessary and not
   sufficient: it never runs the iOS NetworkExtension.

## 3. What is deliberately not claimed

- There is **no generic "ABI version" check**. A gate is per-mirror because only the mirror's author
  knows which fields are semantic. A test that asserts `unsafe.Sizeof(x) == 48` proves the struct
  equals a number somebody typed, not that it equals the toolchain — which is why the oomprofile gate
  parses `$GOROOT` instead.
- The iOS runtime behaviour of these gates is `NOT_RUN` in this repository's CI: there is no device and
  no signed NetworkExtension in the push gate. Everything above is host/compile evidence.
- `runtime.g` has no static layout gate. It cannot: the `g` struct is not exported and its size is not
  reachable from outside the runtime, which is exactly why the implementation probes for `gopc` and
  `startpc` at run time. The behavioural gate in §1.2 is the strongest available check.
