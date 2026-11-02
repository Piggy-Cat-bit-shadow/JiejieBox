# Protocol upgrade and upstream optimisation — final closeout

| | |
|---|---|
| Project | JiejieBox protocol upgrade / upstream sync |
| Branch | `testing` (default; no `main`) |
| Date | 2026-11-02 |
| Final status | **CLOSED — Native Naive security updates explicitly deferred to the next Chromium upgrade** |
| Code head | `d3f38495` |
| Docs closeout | `decdcef4` and this record |

## Outcome

| Work item | Final result |
|---|---|
| Mieru `v3.39.0` | completed |
| Naive/Cronet DNS and dual-stack resolution | completed |
| Naive server CONNECT error handling | completed |
| XHTTP error propagation and lifecycle | completed |
| XHTTP performance evaluation | completed; existing serial design retained |
| VLESS Vision close fix | completed |
| MPTCP listener security hardening | completed |
| Upstream gap audit, dependency locks, CI | completed |
| Documentation and current-tree privacy cleanup | completed |
| NaiveProxy `-3/-4/-5` native patches | **deferred by decision to the next Chromium upgrade** |

## The one explicit boundary

The `-5` eMudhra compromised-intermediate blocklist is **not** in the current native libraries.
This is an accepted, recorded risk, not a claim that the libraries are current:

* current native pins remain `8c68ce8` for the 29 controlled `lib/*` modules; root/all Go binding
  remains `bd2b70516b92`;
* official blocklist commit `23953c90e3b9f08f8d0c6ede92ae35f357af6f73` is not in the
  `cronet-go-dev-v154` branch (`be1be963`), and `git grep -i eMudhra` over its cert sources is
  empty;
* the newer prebuilt `d5437a0` changes only Apple/tvOS archives and contains no blocklist;
* this machine lacks `gn`, `ninja`, `gclient` and `depot_tools`, so the controlled Chromium rebuild
  cannot be produced here.

**Mandatory gate for the next Chromium upgrade** — a newer Chromium version alone does not close
this item:

1. Confirm the eMudhra blocklist commit (or a reviewed equivalent) is actually present in the new
   native source; do not infer it from the version number.
2. Audit the official `-3`/`-4` fixes separately: QUIC trusted roots, dynamic receive-window
   scaling/settings, forced tunneling, old-tunnel cleanup, queued-read crash. Do not assume the
   upstream branch already carries them.
3. Rebuild all 29 controlled archives, then refresh the root/test `go.mod`/`go.sum` pins,
   `release/source-lock.json`, `release/cronet-provenance.txt` and `cronet_native.shas` from that
   exact build.
4. Re-run provenance, release-freeze, the affected Go packages, `-race` and CI before claiming the
   native update complete.

## Privacy boundary

The current tracked tree was scrubbed of real deployment names, addresses, SSH port details and
local workspace paths. Old Git history still contains earlier metadata: it was **not** rewritten,
because that requires a coordinated history rewrite and force-push. Current-tree cleanliness and
historical metadata are separate states and are recorded as such.

## Final statement

The six-stage protocol work, targeted Debug, regressions, security hardening and CI verification are
complete. The only deferred item is the native NaiveProxy `-3/-4/-5` refresh, explicitly scheduled
for the next Chromium upgrade with the mandatory checks above. No release, tag, deployment or VPS
action was performed.
