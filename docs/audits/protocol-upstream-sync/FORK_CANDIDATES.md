# Fork candidates and how to use them

Snapshot: 2026-11-02. This file is the "who already did it" half of `UPSTREAM_LEDGER.md`: fork
heads, maintenance state, the files worth reading, and the reuse cautions. It is an inventory, not
an import list — nothing here is approved for a mechanical cherry-pick.

## 1. Matrix

| Fork | Head at audit | Base / policy | License | Material for this project | Verdict |
|---|---|---|---|---|---|
| `SagerNet/sing-box` (official) | `e5d91fa79f6319d5de940d6c81d81f0fe53b9476` (2026-10-28); stable `v1.14.4` (2026-10-24) | upstream of this fork | GPL-3.0 | protocol wrappers (Hysteria2/TUIC/AnyTLS/VLESS/SS/Snell), lifecycle refactor `a691a440…`, MASQUE on `dev` only (`306235e…`) | reference baseline, not a merge target |
| `Leadaxe/sing-box-lx` | `a52ff73e8ae54a928c9d36fe1326938e3831622d` (2026-11-01, `v1.14.4-lx.28`) | sing-box v1.14.4 + lx SPEC series | GPL-3.0 | XHTTP client audit + packet-up pipeline (lx.25: `f2e24e1`, `fad4fd0`, `21f7178`); MASQUE close wedge (`1858657b`, lx.28) | **primary fork oracle for 04/05/06** |
| `flyzstu/xhttp-box` | `2e7e53d49333999767f1343aa76e5f4b2cc3217a` (2026-10-31) | replanted onto sing-box v1.14.4 (`597f5303`, 2026-10-30) | GPL-3.0 | independent `transport/v2rayxhttp`, weighted group, log rotation | second XHTTP oracle; compare trees, not history |
| `ushan0v/sing-box-forkop` | `d8b41b1d4073555ac2e59df6b553f970d31c7220` (2026-10-31) | sing-box `testing` + group/subscription work | GPL-3.0 | `protocol/mieru` adapter pinned to `mieru v3.33.0`; no fallback/generation lifecycle | read-only comparison; no patch to reuse |
| `uskg8/sing-box-extended` | `ff27faa0b756b6a646e5e98057296a3137b8c75f` (2025-10-12) | stale ≈12.7 months | GPL-3.0 | none (no `protocol/mieru`; pre-dates the current forks) | **not a candidate** |
| `Piggy-Cat-bit-shadow/cronet-go` (this project's fork) | `bd2b70516b927e788b2dab91c17984d3d3a2d052` (2026-11-01) | Chromium `154.0.8037.49` submodule `97ae698` + JiejieBox Naive patches; four Go modules + controlled native archives | GPL-3.0 (SagerNet wrapper; Chromium parts keep their own notices) | the actual Naive client engine; now also carries the port of upstream `fa319c4` (dialer-side server resolution) | **must not be "upgraded" by version number** |
| `SagerNet/cronet-go` (upstream) | `dev` head `fa319c4f3b554bd7c647c6e0f18a7195ac1497a6` (2026-10-23); latest tag `v150.0.7871.63-3` = `049f290970…` (2026-09-12) | upstream of the fork | GPL-3.0 | the Sept lifecycle fixes are ancestors of the fork; `fa319c4` was ported | main tag line is older than the fork's Chromium base |
| `klzgrad/naiveproxy` | `6acbded5dc48d0c5abcb08364c6bcdbf451329b1` (2026-11-01, docs) | Chromium client patches; 154 series | BSD-3-Clause | QUIC window/heartbeat, tunnel cleanup, cert block list | phase 02 source comparison; the fork's submodule is pre `-3`/`-4`/`-5` |
| `klzgrad/forwardproxy` | `e747854e78743dba85c039b38ed185fb11d93667` (2026-10-22) | Caddy plugin | Apache-2.0 | authenticated-CONNECT idle semantic (`e747854`), sing-quic BBRv1 (`c096d6a`) | phase 03 semantic reference |
| `XTLS/Xray-core` | `a4a3dff5b20c2faae567b742c172c7b95e75c083` (2026-10-31, `v26.10.31`) | protocol authority | MPL-2.0 | XHTTP `h2Params`/`OPTIONS`/`WaitReadCloser`; VLESS/REALITY; MASQUE client | protocol semantics, not a code base to merge |
| `HyNetworks/hysteria` | `fb25dc852ebbc70658e67889f411887a82b7c281` (2026-10-27); stable `app/v2.13.0` | reference implementation (Rust) | MIT (`UNVERIFIED` this run) | sniffing/port-hopping/ECH semantics | wire reference only |
| `tuic-protocol/tuic` | `8e118f242f24a17a9f487dc344cc50d7e63e557e` (2025-03-13) | dormant | GPL-3.0 (`UNVERIFIED`) | `SPEC.md` | frozen reference |
| `anytls/anytls-go` | `fd6167acd6d73b9fa3e607659951847fbc9e6c50` (2026-08-03), `v0.0.13` | dormant; superseded in practice by `SagerNet/sing-anytls` | MIT (`UNVERIFIED`) | original framing | not the implementation of record |
| `shadowsocks/shadowsocks-rust` | `e697eb292b66e1e3977c4e7dbd2026ca7842ef1e` (2026-10-22) | reference implementation (Rust) | MIT (`UNVERIFIED`) | half-close idle bound (`dc43297`) | behaviour reference only |

## 2. What each candidate is actually good for

### `Leadaxe/sing-box-lx`

* **XHTTP** — lx.25 is the design JiejieBox's `transport/v2rayxhttp` comments already name
  (`SPEC 002/061/072/076/077/082/094/104`). Read `f2e24e1` (client audit against Xray) and
  `fad4fd0`/`21f7178` (packet-up pipeline) when auditing phase 04/05. The specific gap already
  identified is `dialStreamUp`'s ignored upload status (`conn.go:224-231`).
* **MASQUE** — `1858657b` (lx.28) is the only fork-level close/lifecycle fix found, and its failure
  mode (a detour conn whose read deadline is a no-op) is reproducible locally. Use its test shape
  as an adversarial recipe; do not assume JiejieBox has the same bug.
* **Caution** — the fork carries a large SPEC/TASKS documentation tree and its own group/DNS/MTU
  design. Import patches or test ideas, never the architecture.

### `flyzstu/xhttp-box`

* Independent `transport/v2rayxhttp` implementation on the same sing-box version; the best oracle
  for "does the other client also do this?" after the lx comparison.
* The repo history was rewritten by the 2026-10-30 replant (`597f5303`). Compare trees/blobs at
  `2e7e53d`, not commit ancestry.
* Its weighted group and log-rotation work is out of scope for the protocol phases.

### `ushan0v/sing-box-forkop`

* The only other `protocol/mieru` adapter found. It pins `mieru v3.33.0` and has no fallback,
  generation or drain logic, so it cannot supply a patch for JiejieBox's lifecycle. It is useful
  for one question only: whether JiejieBox's adapter is doing something the SDK already does.
* No action in phase 01.

### Official sing-box `dev` (MASQUE premise correction)

* `306235e` (2026-10-23) is real but **dev-only**; `protocol/masque` and `transport/masque` do not
  exist at `v1.14.4`. For phase 06, the alignment target is the shared `transport/http` capsule and
  client layer plus the endpoint option shape — not a green-field MASQUE project and not a
  replacement for JiejieBox's endpoint.

### `Piggy-Cat-bit-shadow/cronet-go`

* The four-module pin (`all` + native libs) is controlled: `release/source-lock.json` records the
  native archive SHA `8c68ce89873c` and `release/dependencies.json` marks `cronet_native` as
  `controlled` (29 modules). Moving the Go module without rebuilding and re-verifying the archive
  would ship new bindings against old binaries — explicitly forbidden.
* The Sept fixes are ancestors; the fork now also carries a port of upstream `fa319c4`, and the
  native submodule (`97ae698`, Chromium `154.0.8037.49`) predates the official `-3`/`-4`/`-5`
  tags. Compare **source pins, submodule and patch content**, not Chromium version numbers.
* Do not add a Go-level certificate-name block to imitate upstream `-5`; the verification happens in
  the native chain.

## 3. Licensing rules for reuse

* **GPL-3.0 forks** (`sing-box`, `sing-box-lx`, `xhttp-box`, `forkop`, `sing-*` modules,
  `cronet-go`): same license as this project; direct reuse is possible, attribution/headers must be
  preserved. Chromium-derived native code inside cronet-go keeps its own notices.
* **MPL-2.0** (`Xray-core`): file-level copyleft. A ported file must keep the MPL notice and its
  source must remain available; do not copy Xray files into GPL-only files without recording it.
* **BSD-3-Clause / Apache-2.0** (`naiveproxy`, `forwardproxy`): permissive; keep the copyright
  notice. Apache-2.0 is compatible with GPLv3, but the patent clause travels with the file.
* A row marked `UNVERIFIED` in the license column must be checked before code is copied.

## 4. Evaluation checklist (apply before any port)

1. Is the upstream change a **behaviour fix** or only an API/refactor shape?
2. Does JiejieBox already implement the behaviour under a different structure? (Search the ledger's
   file/symbol column first; then `KNOWLEDGE/INDEX.md`.)
3. Does the change touch a load-bearing boundary (`Scope`, generation/lease, buffer ownership,
   QUIC/H3 ownership, ABI/build tags)? If yes, it needs a design note, not a patch.
4. Can the behaviour be pinned by a local test that fails before and passes after? If not, the row
   stays `DEFERRED`.
5. Does the change require a controlled native archive move? If yes, it is
   `BLOCKED_BY_ENVIRONMENT` until a Chromium toolchain exists.
