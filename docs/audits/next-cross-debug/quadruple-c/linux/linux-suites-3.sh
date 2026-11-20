#!/usr/bin/env bash
# Install the missing capture dependency and re-run the package that was blocked by it; also report the
# staged fork's revision so the evidence names the exact tree.
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
export GOFLAGS=-mod=mod
export GOTOOLCHAIN=local
export PATH=$PATH:/usr/sbin:/sbin

echo "=== install tcpdump ==="
if ! command -v tcpdump >/dev/null 2>&1; then
  apt-get update -qq >/dev/null 2>&1
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq tcpdump >/dev/null 2>&1
fi
command -v tcpdump && tcpdump --version | head -n 1

echo "=== tlsspoof integration tests with tcpdump present ==="
cd /root/jjb
tags=$(cat release/DEFAULT_BUILD_TAGS_OTHERS)
go test -count=1 -timeout 600s -tags "$tags" ./common/tlsspoof/ 2>&1 | tail -n 30

echo "=== fork revision under test ==="
git -C /mnt/c/Deepseek/sing-tun rev-parse HEAD
git -C /mnt/c/Deepseek/sing-tun log --oneline -1
echo "=== lock pin ==="
grep -A3 'sing-tun' /root/jjb/release/source-lock.json | head -n 8
