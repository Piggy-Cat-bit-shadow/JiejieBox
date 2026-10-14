# WINDOWS_CORE_MASQUE_GATE_FIX

```text
WINDOWS_CORE_GATE_FIX = COMPLETE AND VERIFIED
WINDOWS_CORE_RUN      = 38445964597 (#37) at 6737404d1  ->  success
PRODUCT_CODE_CHANGED  = NO
FULL_RELEASE_REBUILD  = NOT RUN (as instructed)
```

## 1. The gate was reading the wrong platform — confirmed, not inferred

`windows-core-amd64.yml`'s capability step ran

```sh
go list -deps -tags "$BUILD_TAGS" ./cmd/sing-box | grep -qx '.../protocol/masque'
```

with **no target named**. `go list` takes GOOS/GOARCH/CGO_ENABLED from *its own* environment, and the
build step's `export` does **not** cross a GitHub Actions step boundary — every step is a fresh
process. So the check described the RUNNER's graph, not the shipped artifact's.

That is the user's hypothesis and it is what the failing log says:

```text
GOTOOLDIR='/opt/hostedtoolcache/go/1.26.8/x64/pkg/tool/linux_amd64'   <- LINUX toolchain
release profile: protocol/naive compiles -> auth.go inbound.go ... outbound.go   <- PASSED
control profile: protocol/naive ignores  -> ... outbound.go                      <- PASSED
control is sound: ...
FAIL: protocol/masque is not in the release build's package graph
```

The two `protocol/naive` checks in the same step **passed** because they ask a platform-independent
question (which files a tag set selects). The graph check is the only one whose answer depends on
GOOS, and it is the only one that failed. Linux and Windows carry different QUIC/nftables paths.

It was also **non-deterministic**: `windows-core` #34 and #35 succeeded on the same code, #36 failed.
That is consistent with a host-graph lookup whose result depends on cache and module-resolution
state, and it is why the failure looked like it had been introduced by an unrelated commit.

`protocol/masque` **is** in the real Windows release graph. No MASQUE product code was touched, and
no build-tag file was touched.

## 2. The fix

The capability step now names its target, using the same workflow-level values the build step uses,
so the checked graph and the built artifact cannot drift:

```yaml
export CGO_ENABLED=0 GOOS="$GOOS_TARGET" GOARCH="$GOARCH_TARGET"
echo "capability check target: $GOOS/$GOARCH CGO_ENABLED=$CGO_ENABLED (host is $(go env GOHOSTOS)/$(go env GOHOSTARCH))"
```

`GOOS_TARGET`/`GOARCH_TARGET` are workflow-level `env` (`windows`/`amd64`), so they are present in
every step. The refusal message now also prints the GOOS/GOARCH it queried and the host it ran on, so
this class of confusion cannot recur silently.

All three `go list` calls in this workflow are now explicitly targeted — verified by scanning the
file for untargeted `go list|build|vet` per step: **none remain**.

## 3. Audit of the remaining release gates — so this is not whack-a-mole

The same four questions were asked of every release workflow. **`windows-core-amd64.yml` was the only
one that was wrong**, and the reason is structural: it is the only job whose declared target
(`windows`) differs from the runner (`linux`).

```text
gate                              verdict
windows-core-amd64.yml            WAS WRONG  - fixed and verified (#37 success)
server-linux-amd64.yml:466        LATENT     - go list -deps with no target; correct today only
                                    because the runner IS linux/amd64
server-linux-amd64.yml:617        LATENT     - go vet, same reasoning
client-macos.yml:301              LATENT     - CGO_ENABLED=1, no GOOS; correct only because the
                                    runner IS darwin/arm64
android-core-arm64.yml:207        SAFE       - go list -m -f {{.Version}} asks the module graph,
                                    which is platform-independent
client-desktop-windows.yml        SAFE       - no go tooling
release.yml                       SAFE       - no go tooling; git rev-parse HEAD is the tag
                                    checkout release.yml itself validates
go version -m (all workflows)     SAFE       - reads metadata EMBEDDED in the artifact, not a
                                    host graph
```

The three LATENT entries are **recorded, not changed.** They are working, and rewriting three
working checks during a release is not warranted. They are named here so that a runner-image change
fails visibly at the check rather than silently answering about the wrong platform.

## 4. What this changes for the release

`windows-core` #36 is a **CI gate defect**, not a product defect, and the artifact it produced was
never invalid — the gate simply misjudged it. With the gate fixed and #37 green, the Windows core
product passes for the SHA that carries the fix.

The release still needs the five product workflows to be green **at one SHA**, and the fix commit
`6737404d1` is now that SHA's candidate. Those runs have not been re-dispatched, per instruction.

No tag was moved in this round, no Release was created, and nothing was uploaded.
