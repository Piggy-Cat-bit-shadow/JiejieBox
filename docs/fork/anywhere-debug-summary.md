# Anywhere debug round — delivery summary

Companion to [`anywhere-debug-audit.md`](anywhere-debug-audit.md) and the round-1 construction
[`anywhere-audit-and-construction.md`](anywhere-audit-and-construction.md).

## What was fixed

One real regression introduced by the round-1 streaming load:

**A remote binary rule-set whose body is truncated after the last rule was accepted.** `srs.Read`
stops at the end of the declared rules; the round-1 loader published as soon as it returned, while the
read-all path it replaced consumed the body to EOF, returned `unexpected EOF`, and kept the previous
generation. Fix (`1f1775e37d2`): drain the body before publish and return the drain error. Added the
empty/garbage cases as well (`742da5e8df3`).

## Behaviour and cost

* Behaviour is back to the pre-round-1 contract for a truncated download: the refresh fails, the old
  rules stay live, the new generation is not published.
* No configuration/interfaces changed; the cache, source/JSON, `initial_path` and cache-restore paths
  are untouched.
* Peak memory win stands: the 4 MiB fixture allocated **21,229,120 B** before round 1 and
  **~125 KB** now (`io.Copy`'s small buffer included), with the 4 MiB tail never held as a slice.
* The drain reads the remainder, exactly as the pre-round-1 `io.ReadAll` did; it is not a new
  bandwidth cost relative to the old behaviour, and it is not claimed as a keep-alive optimisation
  (`fetch` closes idle connections after non-initial refreshes anyway).

## What was checked and found clean

Empty/garbage bodies, truncation inside the rules, the cache and source branches, `initial_path`,
partial publication on build/validate errors, callback ordering, concurrent fetch (not reachable),
`with_low_memory`, and the allocation bound. Details and evidence levels are in the audit report.

## What was not verified

* `-race`: blocked (no cgo toolchain); the change adds no shared state, but this is reasoning.
* Device `phys_footprint` on iOS/Android: not run (no device/SDK).
* HTTP/2 and HTTP/3 server variants were not separately simulated.
* The local `scripts/ci/test-low-memory.sh` needs bash and was not run, **but its remote counterpart —
  the `Apple tags and the low-memory data paths` CI job — passed on the pushed SHA**, including
  `with_low_memory`. The same job resolves the local Windows linker limitation for the canonical Apple
  tag set.

## Git

* Debug start: `93ca5351cb20ef659879273b2b4616c5a69a04a1` (clean, `testing`, remote equal).
* Fix: `1f1775e37d257f6c66cb849478fa57b5c0592eae`; tests: `742da5e8df3f3429002e3924ebfcd6933777c47a`.
* CI on `48a58f42da3` (code + debug docs): `verify`, `Android ARM64 core` and `Apple tags and the
  low-memory data paths` all **success**.
* This report set: the documentation commit that contains it.
* Branch `testing`; no force push, no history rewrite, no new branch. `git log --oneline 93ca535..HEAD`
  is the exact range.
