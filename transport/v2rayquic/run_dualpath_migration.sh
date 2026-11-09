#!/bin/bash
# Isolated two-path migration evidence for the V2Ray QUIC transport.
#
# Two network namespaces joined by two independent veth links. The server holds ONE address
# reachable over both links; the client dials it from a different source address per link. The
# test binary then physically takes the first link down and has to keep the already-open flow
# alive by moving it to the second link.
#
# Everything runs inside `unshare -Urn` (user + network namespace), so no root on the host is
# needed. Run from WSL: bash run_dualpath_migration.sh <evidence-dir>
set -u

EVIDENCE_DIR="${1:-$PWD}"
mkdir -p "$EVIDENCE_DIR"
LOG="$EVIDENCE_DIR/dualpath.log"
: >"$LOG"

log() { echo "$@" | tee -a "$LOG"; }

BINARY="$EVIDENCE_DIR/v2rayquic-ns.test"
log "=== build test binary ==="
if ! (cd /mnt/c/Deepseek/_next/network && \
  PATH=/home/jie/go-toolchain/bin:$PATH go test -c -buildvcs=false -tags with_quic \
    -o "$BINARY" ./transport/v2rayquic/ >>"$LOG" 2>&1); then
  log "BUILD FAILED"
  exit 1
fi
log "build: $(ls -l "$BINARY" | awk '{print $5" bytes"}')"

log "=== topology (inside unshare -Urn) ==="
unshare -Urn bash -s -- "$BINARY" "$EVIDENCE_DIR" "$LOG" <<'INNER'
set -u
BINARY="$1"
EVIDENCE_DIR="$2"
LOG="$3"

SERVER_IP=10.99.1.2

unshare -n sleep 300 &
SRV=$!
unshare -n sleep 300 &
CLI=$!
trap 'kill $SRV $CLI 2>/dev/null' EXIT
sleep 0.5

# Two independent links. Link A is the one that goes down; link B carries the flow afterwards.
ip link add va1 type veth peer name va2
ip link add vb1 type veth peer name vb2
ip link set va2 netns "$SRV"
ip link set vb2 netns "$SRV"
ip link set va1 netns "$CLI"
ip link set vb1 netns "$CLI"

# Server: the service address is 10.99.1.2 on the A side, and the SAME address is also held on the
# B side, so one peer address is reachable over both links. arp_ignore/arp_announce let it answer
# for that address on either link.
nsenter -t "$SRV" -n ip addr add 10.99.1.2/24 dev va2
nsenter -t "$SRV" -n ip addr add 10.99.2.2/24 dev vb2
nsenter -t "$SRV" -n ip addr add "$SERVER_IP"/32 dev vb2
nsenter -t "$SRV" -n ip link set va2 up
nsenter -t "$SRV" -n ip link set vb2 up
nsenter -t "$SRV" -n ip link set lo up
nsenter -t "$SRV" -n sysctl -qw net.ipv4.conf.all.arp_ignore=1
nsenter -t "$SRV" -n sysctl -qw net.ipv4.conf.all.arp_announce=2

# Client: one address per link, and a source-based route so a socket bound to the B address leaves
# on the B link while still targeting the same peer address.
nsenter -t "$CLI" -n ip addr add 10.99.1.1/24 dev va1
nsenter -t "$CLI" -n ip addr add 10.99.2.1/24 dev vb1
nsenter -t "$CLI" -n ip link set va1 up
nsenter -t "$CLI" -n ip link set vb1 up
nsenter -t "$CLI" -n ip link set lo up
nsenter -t "$CLI" -n ip route add "$SERVER_IP"/32 dev vb1 src 10.99.2.1 table 100
nsenter -t "$CLI" -n ip rule add from 10.99.2.1 lookup 100

echo "--- server namespace ---" | tee -a "$LOG"
nsenter -t "$SRV" -n ip addr show | tee -a "$LOG"
nsenter -t "$SRV" -n ip route show | tee -a "$LOG"
echo "--- client namespace ---" | tee -a "$LOG"
nsenter -t "$CLI" -n ip addr show | tee -a "$LOG"
nsenter -t "$CLI" -n ip route show | tee -a "$LOG"
nsenter -t "$CLI" -n ip rule show | tee -a "$LOG"

echo "--- reachability before the test ---" | tee -a "$LOG"
nsenter -t "$CLI" -n ping -c1 -W1 -I 10.99.1.1 "$SERVER_IP" >/dev/null && echo "PATH_A_REACHABLE" | tee -a "$LOG"
nsenter -t "$CLI" -n ping -c1 -W1 -I 10.99.2.1 "$SERVER_IP" >/dev/null && echo "PATH_B_REACHABLE" | tee -a "$LOG"

STATE=$(mktemp -d)
export STATE
echo "STATE=$STATE" | tee -a "$LOG"

echo "=== server role ===" | tee -a "$LOG"
nsenter -t "$SRV" -n env \
  V2RAYQUIC_NS_DUALPATH=1 V2RAYQUIC_NS_ROLE=server \
  V2RAYQUIC_NS_STATE="$STATE" V2RAYQUIC_NS_ADDR="$SERVER_IP" \
  "$BINARY" -test.run '^TestIsolatedDualPathMigration$' -test.v -test.timeout 180s \
  >>"$LOG" 2>&1 &
SERVER_TEST=$!

echo "=== client role ===" | tee -a "$LOG"
nsenter -t "$CLI" -n env \
  V2RAYQUIC_NS_DUALPATH=1 V2RAYQUIC_NS_ROLE=client \
  V2RAYQUIC_NS_STATE="$STATE" V2RAYQUIC_NS_ADDR="$SERVER_IP" \
  V2RAYQUIC_NS_LOCAL_A=10.99.1.1 V2RAYQUIC_NS_LOCAL_B=10.99.2.1 \
  "$BINARY" -test.run '^TestIsolatedDualPathMigration$' -test.v -test.timeout 180s \
  >>"$LOG" 2>&1
CLIENT_RC=$?

wait "$SERVER_TEST" 2>/dev/null
echo "CLIENT_EXIT=$CLIENT_RC" | tee -a "$LOG"
echo "--- link A state on the client after the test ---" | tee -a "$LOG"
nsenter -t "$CLI" -n ip link show va1 | tee -a "$LOG"
echo "--- counters ---" | tee -a "$LOG"
cat "$STATE/counters" 2>/dev/null | tee -a "$LOG"
echo | tee -a "$LOG"

if [ "$CLIENT_RC" -ne 0 ]; then
  echo "RESULT=FAIL" | tee -a "$LOG"
  exit 1
fi
echo "RESULT=PASS" | tee -a "$LOG"
INNER
