#!/usr/bin/env bash
# The retirement arm, with the durable record actually written (graceful settle before terminating).
# C1 issues and uses a placeholder; C2 runs a DIFFERENT range over the same cache file; the old
# placeholder must then be refused because the generation that issued it is retired - not because the
# record is missing, which is the diagnostic the earlier short-settle run produced.
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
export PATH=$PATH:/usr/sbin:/sbin
WORK=/root/q01
NAMESPACE=q01
rm -f $WORK/cache.db $WORK/*.log
ip netns del $NAMESPACE 2>/dev/null
ip netns add $NAMESPACE
ip netns exec $NAMESPACE ip link set lo up

cat >$WORK/retire.sh <<'INNER'
#!/usr/bin/env bash
set -uo pipefail
WORK=/root/q01
$WORK/q01client echo 127.0.0.1:18081 >$WORK/echo.log 2>&1 &
ECHO_PID=$!
sleep 0.3
echo "=== arm 1: C1 issues and uses a placeholder ==="
$WORK/sing-box run -c $WORK/config-c1.json -D $WORK >$WORK/r1.log 2>&1 &
SB=$!
sleep 2
OLD=$($WORK/q01client resolve 8.8.8.8:53 alpha.test 2>/dev/null)
echo "placeholder: $OLD"
$WORK/q01client dial "${OLD}:18080" >$WORK/r1dial.log 2>&1
echo "dial exit=$? out=$(cat $WORK/r1dial.log)"
sleep 3
kill -TERM $SB; wait $SB 2>/dev/null; sleep 1
echo "=== arm 2: C2 with a different range, same cache file ==="
$WORK/sing-box run -c $WORK/config-c2.json -D $WORK >$WORK/r2.log 2>&1 &
SB=$!
sleep 2
$WORK/q01client dial "${OLD}:18080" >$WORK/r2dial.log 2>&1
echo "retired dial exit=$? out=$(cat $WORK/r2dial.log)"
$WORK/q01client dial "198.19.0.7:18080" >$WORK/r2b.log 2>&1
echo "unassigned dial exit=$? out=$(cat $WORK/r2b.log)"
sleep 0.3
echo "echo accepts total: $(grep -c 'echo accept' $WORK/echo.log)"
echo "--- C2 log: retirement diagnostics ---"
grep -Ei 'fakeip|retire|refus|missing|reject' $WORK/r2.log | tail -n 12
kill -TERM $SB; wait $SB 2>/dev/null
kill $ECHO_PID 2>/dev/null
sleep 0.2
INNER
chmod +x $WORK/retire.sh
ip netns exec $NAMESPACE bash $WORK/retire.sh
ip netns del $NAMESPACE 2>/dev/null
echo "=== done ==="
