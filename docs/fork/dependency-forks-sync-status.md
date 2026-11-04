# Dependency forks: measured upstream position

Measured 2026-11-04 by cloning each fork with both remotes and computing
`git rev-list --left-right --count origin/<branch>...upstream/<upstream-branch>`.
The GitHub compare API is not used: it reports 0/0 for cross-fork comparisons here even when the
real answer is 33 behind (sing-tun), and returns nothing at all for some repositories.

| Fork | Branch | HEAD | Ahead | Behind |
|---|---|---|---|---|
| `sing` | `dev` | `1729220` | 55 | **0** |
| `sing-quic` | `main` | `1ef2597` | 7 | **0** |
| `sing-tun` | `dev` | `e9c7af7` | 134 | **0** |
| `quic-go` | `sagernet` | `2daa76f5` | 18 | **0** |
| `cronet-go` | `main` | `d841c37` | 16 | **0** |
| `utls` | `main` | `b63a2e9` | 68 | **0** |
| `sing-box-for-apple` | `main` | `4773b5f` | 22 | **0** |
| `sing-box-for-android` | `main` | `c783710` | 10 | **0** |
| `sing-box-for-desktop` | `main` | `41722b0` | 37 | **0** |
| `sing-box-dashboard` | `main` | `0da3417` | 5 | **0** |

## What each number means

* **Seven forks needed no work.** `sing`, `sing-quic`, `quic-go`, `utls`,
  `sing-box-for-android`, `sing-box-for-desktop` and `sing-box-dashboard` are already 0 behind
  their tracked upstream branch. They are left exactly as they were: a merge that moves nothing is
  not a sync, it is a commit.
* **`sing-tun` was merged** (`e9c7af7`). Its 33 "missing" commits were mostly already ported under
  other SHAs — 86 fork commits carry the same subjects — so the merge records ancestry and the
  conflict resolution kept the fork's superset implementations.
* **`cronet-go` was merged and its native libraries rebuilt** (`d841c37`). Two of three upstream
  commits were genuinely new; the third was this fork's `bd2b705`. The `naiveproxy` submodule moved
  to upstream's pin `7c64cae`.

  The libraries are a separate problem from the merge: the four targets this product links
  (`android/arm64`, `darwin/arm64`, `ios/arm64`, `windows/amd64`) are built from Chromium and had to
  be rebuilt from the synced sources, which took six dispatches because the macOS hosted pool
  cancelled the Apple jobs at the fifteen-minute acquisition timeout five times. The workflow now
  builds only those four by default (`profile=full` for everything), publishes on demand, refuses to
  publish a partial matrix, and the binaries are committed here rather than pushed through the
  publisher: `cmd/build-naive publish` ends with `GOPROXY=direct go mod tidy`, which resolves the
  module path `github.com/sagernet/cronet-go` - i.e. upstream, where this fork's commit does not
  exist. The consuming `replace` directives point at this fork, so committing the artifacts is the
  same content the publisher would have merged.
  The native build is CI's to prove; this host has no C toolchain.
* **`sing-box-for-apple` was integrated** (`4773b5f`). Its `main` had merged upstream without the
  fork's loopback and self-sign work; the integration puts upstream's four commits, the startup
  fixes and the loopback feature at one revision, and both iOS and macOS build from it.

## Reproduce

```bash
dir=<clone of the fork>
git -C "$dir" fetch origin
git -C "$dir" fetch upstream <upstream-branch>
git -C "$dir" rev-list --left-right --count origin/<branch>...upstream/<upstream-branch>
```

## Not claimed

`behind = 0` is an ancestry statement, not a behaviour statement: it says the fork contains
upstream's commits, not that every upstream behaviour was exercised here. Nothing in this table was
built or tested on a device; the Go forks were built where noted in their own reports.
