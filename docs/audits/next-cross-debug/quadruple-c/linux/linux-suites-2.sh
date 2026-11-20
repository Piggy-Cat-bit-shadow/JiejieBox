#!/usr/bin/env bash
# Second batch: the Linux route stack, the fork's netlink/TUN tests, and the capture packages that the
# Windows session could not run.
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
export GOFLAGS=-mod=mod
export GOTOOLCHAIN=local

cd /root/jjb
tags=$(cat release/DEFAULT_BUILD_TAGS_OTHERS)
echo "=== route (Linux-specific: fd disposal, family route enumerate/filter) ==="
go test -count=1 -timeout 2400s -tags "$tags" ./route/ 2>&1 | tail -n 60
echo "=== tlsspoof / netcap ==="
go test -count=1 -timeout 600s -tags "$tags" ./common/tlsspoof/ ./common/netcap/ 2>&1 | tail -n 40

echo "=== sing-tun fork (netlink monitor, UDP NAT, system route) ==="
if [ ! -d /root/sing-tun ]; then
  mkdir -p /root/sing-tun
  rsync -a --exclude=.git /mnt/c/Deepseek/sing-tun/ /root/sing-tun/
fi
cd /root/sing-tun
git_head=$(cat /mnt/c/Deepseek/sing-tun/.git/HEAD 2>/dev/null || true)
echo "fork staged at: $git_head"
go test -count=1 -timeout 1200s ./... 2>&1 | tail -n 60
