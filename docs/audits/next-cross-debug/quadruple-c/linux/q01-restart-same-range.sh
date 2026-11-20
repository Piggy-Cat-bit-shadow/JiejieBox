#!/usr/bin/env bash
# Is the placeholder attributable after a GRACEFUL restart? The async cache-file writer batches its
# writes, so this run waits well past the flush window before terminating, then restarts with the same
# range and dials the old placeholder again.
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
export PATH=$PATH:/usr/sbin:/sbin
WORK=/root/q01
NAMESPACE=q01
rm -f $WORK/cache.db $WORK/*.log
ip netns del $NAMESPACE 2>/dev/null
ip netns add $NAMESPACE
ip netns exec $NAMESPACE ip link set lo up

cat >$WORK/graceful.sh <<'INNER'
#!/usr/bin/env bash
set -uo pipefail
WORK=/root/q01
$WORK/q01client echo 127.0.0.1:18081 >$WORK/echo.log 2>&1 &
ECHO_PID=$!
sleep 0.3
echo "=== arm 1: assign and use, then let the writer settle ==="
$WORK/sing-box run -c $WORK/config-c1.json -D $WORK >$WORK/g1.log 2>&1 &
SB=$!
sleep 2
OLD=$($WORK/q01client resolve 8.8.8.8:53 alpha.test 2>/dev/null)
echo "placeholder: $OLD"
$WORK/q01client dial "${OLD}:18080" >$WORK/g1dial.log 2>&1
echo "dial exit=$? out=$(cat $WORK/g1dial.log)"
sleep 3
kill -TERM $SB; wait $SB 2>/dev/null; sleep 1
echo "cache size: $(stat -c %s $WORK/cache.db 2>/dev/null)"
echo "=== arm 2: restart same range, dial the old placeholder ==="
$WORK/sing-box run -c $WORK/config-c1.json -D $WORK >$WORK/g2.log 2>&1 &
SB=$!
sleep 2
$WORK/q01client dial "${OLD}:18080" >$WORK/g2dial.log 2>&1
echo "restart dial exit=$? out=$(cat $WORK/g2dial.log)"
sleep 0.3
echo "echo accepts total: $(grep -c 'echo accept' $WORK/echo.log)"
grep -Ei 'fakeip|restor|inbound connection to|outbound connection to|missing' $WORK/g2.log | tail -n 10
kill -TERM $SB; wait $SB 2>/dev/null
kill $ECHO_PID 2>/dev/null
sleep 0.2
INNER
chmod +x $WORK/graceful.sh
ip netns exec $NAMESPACE bash $WORK/graceful.sh
ip netns del $NAMESPACE 2>/dev/null
echo "=== done ==="
