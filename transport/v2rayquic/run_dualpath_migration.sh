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
#
# The verdict is deliberately conjunctive: BOTH roles must pass, each role's own log must contain
# its PASS line, the server must report exactly one handshake and one stream, both paths must have
# been reachable before the change, and the client must have logged a migration. Any one of those
# missing is a FAIL - a green shell exit is not evidence on its own.
set -u

EVIDENCE_DIR="${1:-$PWD}"
mkdir -p "$EVIDENCE_DIR"
LOG="$EVIDENCE_DIR/dualpath.log"
SERVER_LOG="$EVIDENCE_DIR/dualpath-server.log"
CLIENT_LOG="$EVIDENCE_DIR/dualpath-client.log"
: >"$LOG"
: >"$SERVER_LOG"
: >"$CLIENT_LOG"

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
unshare -Urn bash -s -- "$BINARY" "$EVIDENCE_DIR" "$LOG" "$SERVER_LOG" "$CLIENT_LOG" <<'INNER'
set -u
BINARY="$1"
EVIDENCE_DIR="$2"
LOG="$3"
SERVER_LOG="$4"
CLIENT_LOG="$5"

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
  >"$SERVER_LOG" 2>&1 &
SERVER_TEST=$!

echo "=== client role ===" | tee -a "$LOG"
nsenter -t "$CLI" -n env \
  V2RAYQUIC_NS_DUALPATH=1 V2RAYQUIC_NS_ROLE=client \
  V2RAYQUIC_NS_STATE="$STATE" V2RAYQUIC_NS_ADDR="$SERVER_IP" \
  V2RAYQUIC_NS_LOCAL_A=10.99.1.1 V2RAYQUIC_NS_LOCAL_B=10.99.2.1 \
  "$BINARY" -test.run '^TestIsolatedDualPathMigration$' -test.v -test.timeout 180s \
  >"$CLIENT_LOG" 2>&1
CLIENT_RC=$?

wait "$SERVER_TEST" 2>/dev/null
SERVER_RC=$?

echo "CLIENT_EXIT=$CLIENT_RC" | tee -a "$LOG"
echo "SERVER_EXIT=$SERVER_RC" | tee -a "$LOG"
cat "$SERVER_LOG" | tee -a "$LOG"
cat "$CLIENT_LOG" | tee -a "$LOG"

echo "--- link A state on the client after the test ---" | tee -a "$LOG"
nsenter -t "$CLI" -n ip link show va1 | tee -a "$LOG"

COUNTERS_FILE="$STATE/counters"
if [ -f "$COUNTERS_FILE" ]; then
  COUNTERS=$(cat "$COUNTERS_FILE")
else
  COUNTERS=""
fi
echo "COUNTERS=${COUNTERS:-<missing>}" | tee -a "$LOG"
echo | tee -a "$LOG"

# --- the verdict, one condition per line so a failure says which one -----------------------
FAILURES=""
[ "$CLIENT_RC" -eq 0 ] || FAILURES="$FAILURES client-exit($CLIENT_RC)"
[ "$SERVER_RC" -eq 0 ] || FAILURES="$FAILURES server-exit($SERVER_RC)"
grep -q '^--- PASS: TestIsolatedDualPathMigration' "$CLIENT_LOG" \
  || FAILURES="$FAILURES client-no-pass-line"
grep -q '^--- PASS: TestIsolatedDualPathMigration' "$SERVER_LOG" \
  || FAILURES="$FAILURES server-no-pass-line"
grep -q '^PATH_A_REACHABLE' "$LOG" || FAILURES="$FAILURES path-a-not-reachable"
grep -q '^PATH_B_REACHABLE' "$LOG" || FAILURES="$FAILURES path-b-not-reachable"
grep -q 'migration after the link went down took' "$CLIENT_LOG" || FAILURES="$FAILURES no-migration-timing"

if [ -z "$COUNTERS" ]; then
  FAILURES="$FAILURES counters-missing"
else
  COUNTERS_VERDICT=$(python3 - "$COUNTERS_FILE" <<'PY'
import json, sys
try:
    with open(sys.argv[1]) as handle:
        data = json.load(handle)
except Exception:
    print("unreadable")
    raise SystemExit
print("ok" if data.get("handshakes") == 1 and data.get("streams") == 1 else "mismatch:%s" % data)
PY
)
  echo "COUNTERS_VERDICT=$COUNTERS_VERDICT" | tee -a "$LOG"
  [ "$COUNTERS_VERDICT" = "ok" ] || FAILURES="$FAILURES counters-$COUNTERS_VERDICT"
fi

grep -q '^--- FAIL' "$CLIENT_LOG" "$SERVER_LOG" && FAILURES="$FAILURES fail-line-in-role-log"

if [ -n "$FAILURES" ]; then
  echo "RESULT=FAIL" | tee -a "$LOG"
  echo "FAILURES=$FAILURES" | tee -a "$LOG"
  exit 1
fi
echo "RESULT=PASS" | tee -a "$LOG"
INNER
RC=$?
log "HARNESS_EXIT=$RC"
exit "$RC"
