#!/usr/bin/env bash
# Run the Linux-gated test packages that Windows cannot execute. Root is required for netns/TUN/nft.
set -uo pipefail
cd /root/jjb
export PATH=/usr/local/go/bin:$PATH
export GOFLAGS=-mod=mod
export GOTOOLCHAIN=local
tags=$(cat release/DEFAULT_BUILD_TAGS_OTHERS)
echo "tags: $tags"
echo "=== batch 1: namespace / device / syscall packages ==="
go test -count=1 -timeout 900s -tags "$tags" \
  ./common/netns/ ./protocol/bridge/ ./common/listener/ ./common/process/ \
  ./common/ktls/ ./common/trafficsched/ ./common/netx/ 2>&1 | tail -n 40
echo "=== batch 2: tun / quic / http / libbox ==="
go test -count=1 -timeout 1200s -tags "$tags" \
  ./protocol/tun/ ./transport/v2rayquic/ ./transport/http/ ./experimental/libbox/ 2>&1 | tail -n 60
