# Pre-fix race evidence (BUG-002)

Command before the read mutex:

```powershell
$env:PATH = 'C:\src\_toolchain\goroot\bin;C:\msys64\mingw64\bin;' + $env:PATH
$env:CGO_ENABLED = '1'
Set-Location <WORKSPACE_ROOT>\integration-release-next
go test -race ./protocol/mieru -run TestUOTPacketConnConcurrentReads -count=1 -v
```

The detector reported interleaved writes/reads inside `uotFramer.readFrame`; the condensed
trace was:

```text
WARNING: DATA RACE
Write at 0x... by goroutine 13:
  runtime.slicecopy()
  net.(*pipe).read()
  io.ReadAtLeast()
  io.ReadFull()
  github.com/sagernet/sing-box/protocol/mieru.(*uotFramer).readFrame()
      protocol/mieru/uot.go:93
  github.com/sagernet/sing-box/protocol/mieru.(*uotPacketConn).ReadFrom()
      protocol/mieru/packetconn.go:103

Previous read at 0x... by goroutine 16:
  encoding/binary.bigEndian.Uint16()
  github.com/sagernet/sing-box/protocol/mieru.(*uotFramer).readFrame()
      protocol/mieru/uot.go:99
  github.com/sagernet/sing-box/protocol/mieru.(*uotPacketConn).ReadFrom()
      protocol/mieru/packetconn.go:103
```

The test also observed corrupted frames (`prefix 0x01`, `prefix 0x02`, `prefix 0x35`,
`invalid SOCKS5 UDP reserved bytes 00 01`) and missing payloads. After adding
`uotFramer.access sync.Mutex`, the same command passes under `-race` and all eight payloads
are delivered exactly once.

This file is a transcript of a pre-fix run, not a current failure.
