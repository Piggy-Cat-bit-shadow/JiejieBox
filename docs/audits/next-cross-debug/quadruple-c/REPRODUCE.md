# Reproduce — quadruple C (X-Q06: the artifact's ClientHello)

Windows PowerShell. Needs the repository's Go toolchain and a loopback-only listener; nothing leaves the
machine.

## 0. Environment

```powershell
$env:PATH = 'C:\Deepseek\.tools\go1.27.4\go\bin;' + $env:PATH
$env:GOFLAGS = '-mod=mod'
cd C:\Deepseek\JiejieBox
```

## 1. Build the three binaries

```powershell
$tags = (Get-Content release/DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()

# the release recipe's build
$release = '-X github.com/sagernet/sing-box/constant.Version=v0.0.0-audit ' +
           (Get-Content release/LDFLAGS -Raw).Trim() + ' -s -w -buildid='
go build -tags $tags -trimpath -ldflags $release -o C:\Deepseek\.tmp-xq06\sing-box-release.exe ./cmd/sing-box

# a development build (go.mod's `godebug tlssecpmlkem=0` alone)
go build -tags $tags -trimpath -ldflags '-X github.com/sagernet/sing-box/constant.Version=v0.0.0-dev -s -w -buildid=' `
  -o C:\Deepseek\.tmp-xq06\sing-box-dev.exe ./cmd/sing-box

# the defect control: the old cmd/internal/build_shared list
go build -tags $tags -trimpath `
  -ldflags '-X runtime.godebugDefault=multipathtcp=0,tlssha1=1 -checklinkname=0 -s -w -buildid=' `
  -o C:\Deepseek\.tmp-xq06\sing-box-short.exe ./cmd/sing-box
```

Note `-trimpath` is a **build** flag; putting it in `-ldflags` makes the linker fail with a usage dump.

## 2. Measure each binary

```powershell
go run ./scripts/ci/clienthello-artifact-probe C:\Deepseek\.tmp-xq06\sing-box-release.exe
# groups=[4588 29 23 24 25] key_shares=[4588 29]

go run ./scripts/ci/clienthello-artifact-probe C:\Deepseek\.tmp-xq06\sing-box-dev.exe
# groups=[4588 29 23 24 25] key_shares=[4588 29]

go run ./scripts/ci/clienthello-artifact-probe C:\Deepseek\.tmp-xq06\sing-box-short.exe
# groups=[4588 4587 4589 29 23 24 25] key_shares=[4588 29]
```

The third line is the control that proves the probe measures the wire: only the linker string differs.

## 3. What the probe does

1. listens on a random loopback port as the "Trojan server" (the recorder);
2. writes a minimal config into a temp directory: SOCKS inbound on a free port, Trojan outbound with
   TLS pointed at the recorder, `route.final` on the Trojan outbound;
3. starts the binary given on the command line with that config;
4. drives one SOCKS CONNECT to `example.com:443`;
5. reads the first TLS record the binary sends and prints `groups=... key_shares=...`.

The CONNECT is expected to be refused: the recorder closes after reading the ClientHello, so the Trojan
handshake fails. The refusal is not an error for this measurement - the ClientHello has already been
sent.

## 4. Manual variant (no probe)

Useful when the probe's process handling is inconvenient:

```powershell
# terminal 1: the recorder on a fixed port
go run ./scripts/ci/clienthello-artifact-probe --help   # see the doc comment for the config shape
# or simply use the two-process flow described in REPORT.md §1 with any TLS-reading listener
```

## 5. Not reproducible here

X-Q03/X-Q05 need Linux netns/TProxy; X-Q08 needs a DNS64 fabric; X-Q02 needs a provider whose download
is routed through its own group; X-Q04 needs a Mieru SDK harness. Each is recorded with its blocker in
`MATRIX.md` §1.
