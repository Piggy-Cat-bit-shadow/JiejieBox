# Anywhere debug round — delivery summary

Companion to [`anywhere-debug-audit.md`](anywhere-debug-audit.md) and the round-1 construction
[`anywhere-audit-and-construction.md`](anywhere-audit-and-construction.md).

## What was fixed

**1. A real regression introduced by the round-1 streaming load:** a remote binary rule-set whose body
is truncated after the last rule was accepted. `srs.Read` stopped at the end of the declared rules, the
round-1 loader published as soon as it returned, while the read-all path it replaced consumed the body
to EOF, returned `unexpected EOF`, and kept the previous generation. Fix (`1f1775e37d2`): drain the body
before publish and return the drain error. Added the empty/garbage and mid-body cancellation cases
(`742da5e8df3`, `bcc55b7072c`).

**2. A pre-existing parser integrity gap, found by extending the same root cause:** `Read` stopped
after the declared rules and never let zlib verify its Adler-32 trailer. A file cut by up to 8 bytes,
or one with a corrupt trailer, parsed successfully — this affected local files, cache restores and the
remote read-all path, not only the new streaming path. Fix (`6b50757c032`): drain the decompressed
stream after the rules, so zlib checks the checksum and reports truncation. Tests fail on the old
parser (cut 1/2/4 and a flipped last byte) and pass after it.

## Behaviour and cost

* A truncated download now fails and the old generation stays live; a trailer-truncated or
  checksum-corrupt rule-set file is rejected at load instead of being used.
* No configuration/interfaces changed; the cache, source/JSON, `initial_path` and cache-restore paths
  are untouched.
* Peak memory win stands: the 4 MiB fixture allocated **21,229,120 B** before round 1 and
  **~125 KB** now (`io.Copy`'s small buffer included), with the 4 MiB tail never held as a slice.
* The parser drain reads only the remaining compressed data (the footer for a valid file) and uses
  `io.Discard`'s pooled buffer; valid files see no measurable cost.
* The HTTP drain reads the remainder, exactly as the pre-round-1 `io.ReadAll` did; it is not a new
  bandwidth cost relative to the old behaviour, and it is not claimed as a keep-alive optimisation
  (`fetch` closes idle connections after non-initial refreshes anyway).

## What was checked and found clean

Empty/garbage bodies, truncation inside the rules, the cache and source branches, `initial_path`,
partial publication on build/validate errors, callback ordering, concurrent fetch (not reachable),
`with_low_memory`, and the allocation bound. Details and evidence levels are in the audit report.

## What was not verified

* Local `-race` is blocked (no cgo toolchain), but the remote `verify` step ran `-race` over
  `./route/...` on `48a58f4` and again on the parser-fix head `a3761cf`; both passed.
* Device `phys_footprint` on iOS/Android: not run (no device/SDK).
* HTTP/2 is covered by a real TLS/H2 test; HTTP/3 was not separately simulated.
* The local `scripts/ci/test-low-memory.sh` needs bash and was not run. Its remote counterpart, the
  `Apple tags and the low-memory data paths` job, passed on `48a58f4` (the commit that changed
  `route/rule`). It was **not triggered** for the later `common/srs` parser fix because
  `mobile-contract.yml`'s path filter does not include `common/srs/**`; that change is
  platform-independent and is covered by `Verify`. The local Windows linker limitation for the
  canonical Apple tag set is resolved by the `48a58f4` run.

## Git

* Debug start: `93ca5351cb20ef659879273b2b4616c5a69a04a1` (clean, `testing`, remote equal).
* Streaming fix: `1f1775e37d257f6c66cb849478fa57b5c0592eae`; its tests:
  `742da5e8df3f3429002e3924ebfcd6933777c47a`, `bcc55b7072c232ccc60f26800a5c53225a53e33d`.
* Parser checksum fix: `6b50757c032bb83b1cde192398a4ee0bf105a3c7`; comment/CI-trigger:
  `a3761cf51bd3d0015703b56d1e32425a6f52ed12`.
* CI on `48a58f42da3` (streaming fix): `verify`, `Android ARM64 core` and `Apple tags and the
  low-memory data paths` all **success**.
* CI on `a3761cf51bd3` (parser fix): `Verify` **success** (17m 06s, includes the race step) and
  `Android Core ARM64` **success**; the Apple low-memory job was not triggered by its path filter.
* CI on `ad707eb434d` (HTTP/2 tests + docs): `Verify` **success** (19m 25s), `Android Core ARM64`
  **success**, and the Apple tags + low-memory contract **success** (10m 39s).
* This report set: the documentation commit that contains it.
* Branch `testing`; no force push, no history rewrite, no new branch. `git log --oneline 93ca535..HEAD`
  is the exact range.
