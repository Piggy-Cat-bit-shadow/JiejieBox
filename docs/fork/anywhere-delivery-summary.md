# Anywhere round — delivery summary (read this first)

**What this round is:** a source-level study of Anywhere, an audit of this fork against it, and one
measured construction. Full detail: [`anywhere-deep-study.md`](anywhere-deep-study.md) and
[`anywhere-audit-and-construction.md`](anywhere-audit-and-construction.md).

## The construction (the part that changed behaviour)

A **remote binary rule-set without `cache_file`** was downloaded with `io.ReadAll` and then parsed from
the slice. `srs.Read` is already a streaming parser, and the parsed form is compact, so the slice was
pure transient: the whole download was held in the heap while it parsed.

Now `fetch` parses `response.Body` directly when there is no cache to write. A cached profile keeps the
old path exactly (`CacheFile.SaveRuleSet` stores the bytes). Both paths go through one `publishRules`, so
validation and publication cannot drift.

**Measured (same fixture):** a valid `.srs` plus a 4 MiB non-zlib tail allocated **21,229,120 B**
before and **108,376 B** after. The test also matches `10.1.2.3` from the streamed rules. This is a peak
result, not a throughput claim.

## The audit findings that did not become code

* **The splice silent-stall defect recorded in `KNOWLEDGE/` is fixed in the pinned dependency.** The
  source at `sing v0.9.6-0.20261011105649-bde7ddda55ef` sets `handed = true` only after `Pipe2`
  succeeds, and has the regression test and the seam. The knowledge base is stale; source wins. No
  code change was made (it is not this repository's code) and the knowledge base was not refreshed
  as part of normal development.
* **The rule-set representation itself has little left to win.** The existing scale census shows the
  v3+ binary format builds a succinct matcher off the stream; the remaining cost is I/O and the
  transient, which is what the construction removed.
* **Rejected on purpose:** a global buffer ledger (no unbounded queue to enforce), a Swift-style arena
  (Go's `buf.Buffer` ownership already covers it), a custom timer scheduler (Go's timer wheel already
  removes cancelled timers), a second multiplexer pool (violates the single reuse authority), and
  packed-DNS-bytes caching (aliasing risk, not measured). Reasons are in Report 2 §3.3.

## Verification

* `go test ./route/rule -count=1` — PASS (2.157 s).
* `go test -tags with_low_memory ./route/rule -count=1` — PASS (2.288 s); this is the iOS buffer
  geometry.
* `go test ./route -count=1 -short` — PASS (61.099 s); the skipped test deliberately waits six minutes.
* `go test ./route -count=1 -timeout 12m` — full suite, PASS (see Report 2 §5).
* `go vet ./route/rule`, `gofmt -l route/rule`, `go build ./route/...` — clean, with and without
  `with_low_memory`.
* **Not run:** `-race` (no cgo toolchain), `scripts/ci/test-low-memory.sh` (no bash), the iOS build and
  any device `phys_footprint` (no Apple SDK/device). These are `BLOCKED_BY_ENVIRONMENT`/`NOT_RUN`, not
  PASS.

## Git

* Start: `2ed64483a5b7a326e872fb9c35074748c0c5b97c` (clean, `testing`).
* Construction: `d846fb6c8c9eef71f76242f4ba3e64f776c183c1`.
* This report and the two long reports: the documentation commit that contains them.
* Branch `testing`, no force push, no history rewrite. `git log --oneline 2ed6448..HEAD` lists the
  exact range.
