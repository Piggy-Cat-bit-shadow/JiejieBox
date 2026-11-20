# Reproduce — triple-wise A (T11: two build paths, two ClientHellos)

Windows PowerShell. The finding is a build-flag defect, so the reproduction is a measurement, not a
crash.

## 0. Environment

```powershell
$env:PATH = 'C:\Deepseek\.tools\go1.27.4\go\bin;' + $env:PATH
$env:GOFLAGS = '-mod=mod'
cd C:\Deepseek\JiejieBox
```

## 1. Measure the ClientHello a binary offers

A module that mirrors the tree: same language version and the same `godebug` pin.

```powershell
$dir = 'C:\Deepseek\.tmp-cp'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
Copy-Item scripts\ci\clienthello-probe\main.go $dir\main.go
# module: go 1.26.0, toolchain go1.27.4, godebug tlssecpmlkem=0
@"
module probe

go 1.26.0

toolchain go1.27.4

godebug tlssecpmlkem=0
"@ | Set-Content -Encoding utf8 $dir\go.mod
```

Build and run the three strings:

```powershell
cd $dir
go build -o probe-plain.exe .
go build -ldflags '-X runtime.godebugDefault=multipathtcp=0,tlssha1=1' -o probe-old.exe .
go build -ldflags '-X runtime.godebugDefault=multipathtcp=0,tlssha1=1,tlssecpmlkem=0,tracebacklabels=0,x509sslcertoverrideplatform=0' -o probe-fixed.exe .
foreach ($n in 'probe-plain','probe-old','probe-fixed') { & ".\$n.exe" }
```

Measured on Go 1.27.4:

```
probe-plain:  groups=[4588 29 23 24 25]
probe-old:    groups=[4588 4587 4589 29 23 24 25]     <- the defect
probe-fixed:  groups=[4588 29 23 24 25]
```

Control arm (the override dominates the language version):

```powershell
# same directory, change go.mod's `go 1.26.0` to `go 1.25.5`, then:
go build -o p125-plain.exe .
go build -ldflags '-X runtime.godebugDefault=multipathtcp=0,tlssha1=1' -o p125-old.exe .
# p125-plain: groups=[4588 29 23 24 25]
# p125-old:   groups=[4588 4587 4589 29 23 24 25]
```

## 2. See which shipped artifact gets which string

```powershell
# the builder both shipped client paths use:
Get-Content cmd\internal\build_shared\flags.go
# the release recipes' string:
Get-Content release\LDFLAGS
# who calls the builder:
Get-ChildItem -Recurse -Filter *.go | Select-String 'build_shared.LinkerFlags'
Get-ChildItem -Recurse -Include *.yml,*.sh | Select-String 'build_libbox|build_boxdd'
```

`android-core-arm64.yml:256` and `build-apple-libbox.sh:188` run `cmd/internal/build_libbox` (the
Apple/Android library every client links); the desktop client workflow runs `cmd/internal/build_boxdd`;
`windows-core-amd64.yml:201` and the other release recipes build `cmd/sing-box` with
`$(cat release/LDFLAGS)`.

## 3. The regression test (red/green/mutation)

```powershell
go test -count=1 -run 'TestLinkerFlagsCarryTheReleaseGODEBUGDefaults' -v ./cmd/internal/build_shared/
# --- PASS
```

Mutation: change `godebugDefaults` in `cmd/internal/build_shared/flags.go` back to
`"multipathtcp=0,tlssha1=1"`:

```powershell
go test -count=1 -run 'TestLinkerFlagsCarryTheReleaseGODEBUGDefaults' ./cmd/internal/build_shared/
# --- FAIL
# expected: "multipathtcp=0,tlssha1=1,tlssecpmlkem=0,tracebacklabels=0,x509sslcertoverrideplatform=0"
# actual:   "multipathtcp=0,tlssha1=1"
# build_shared and release/LDFLAGS disagree on runtime.godebugDefault ...
```

Restore the file afterwards.

## 4. Package regression

```powershell
go test -count=1 ./cmd/internal/build_shared/ ./cmd/internal/build_libbox/ ./cmd/internal/build_boxdd/
# ok 0.08 s / 2.76 s / 0.08 s
```

## 5. What cannot be reproduced here

- T04 (client-side QUIC migration) does not exist in this fork's client, so there is no interleaving to
  stage; T06 needs a PREF64/NAT64 network; T07 needs a Linux kernel. None is claimed.
