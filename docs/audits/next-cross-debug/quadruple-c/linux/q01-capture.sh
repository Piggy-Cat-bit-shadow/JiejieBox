#!/usr/bin/env bash
# Q01 end to end: fake-IP DNS answers, TUN capture, domain restoration and the router's verdict, all
# observed through real packets inside a private network namespace.
#
# The three systems are (1) the fake-IP store that hands out a placeholder, (2) the TUN capture path that
# must carry the client's packet to sing-box, (3) the router that restores the domain and decides, with
# (4) the real socket the direct outbound opens. The invariant: alpha.test reaches the echo, beta.test is
# refused, and the echo never sees a second connection.
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
export GOFLAGS=-mod=mod
export GOTOOLCHAIN=local
export PATH=$PATH:/usr/sbin:/sbin

WORK=/root/q01
NAMESPACE=q01
mkdir -p $WORK
cd $WORK

echo "=== build sing-box (linux) ==="
if [ ! -x $WORK/sing-box ]; then
  # The TUN inbound needs the gVisor stack; the server profile does not carry it.
  (cd /root/jjb && go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS),with_gvisor" -o $WORK/sing-box ./cmd/sing-box)
fi
ls -l $WORK/sing-box

echo "=== build the client ==="
rm -rf $WORK/client-src && cp -a /mnt/c/Deepseek/.wsl/q01/q01client $WORK/client-src
if [ ! -f $WORK/client-src/go.mod ]; then printf 'module q01client\n\ngo 1.25.0\n' > $WORK/client-src/go.mod; fi
(cd $WORK/client-src && go build -o $WORK/q01client .)

cp /mnt/c/Deepseek/.wsl/q01/config.json $WORK/config.json

echo "=== fresh namespace ==="
ip netns del $NAMESPACE 2>/dev/null
ip netns add $NAMESPACE
ip netns exec $NAMESPACE ip link set lo up

cat >$WORK/run.sh <<'INNER'
#!/usr/bin/env bash
set -uo pipefail
WORK=/root/q01
echo "--- echo server ---"
$WORK/q01client echo 127.0.0.1:18081 >$WORK/echo.log 2>&1 &
ECHO_PID=$!
sleep 0.3
echo "--- sing-box ---"
$WORK/sing-box run -c $WORK/config.json >$WORK/singbox.log 2>&1 &
SB_PID=$!
sleep 2
echo "--- interfaces ---"
ip -br addr
echo "--- routes ---"
ip route
echo "--- resolve alpha.test ---"
ALPHA=$($WORK/q01client resolve 8.8.8.8:53 alpha.test 2>$WORK/alpha.err)
echo "alpha=$ALPHA err=$(cat $WORK/alpha.err)"
echo "--- resolve beta.test ---"
BETA=$($WORK/q01client resolve 8.8.8.8:53 beta.test 2>$WORK/beta.err)
echo "beta=$BETA err=$(cat $WORK/beta.err)"
echo "--- dial the alpha placeholder at :18080 ---"
$WORK/q01client dial "${ALPHA}:18080" >$WORK/dial-alpha.log 2>&1
echo "alpha dial exit=$? out=$(cat $WORK/dial-alpha.log)"
echo "--- dial the beta placeholder at :18080 (must be refused) ---"
$WORK/q01client dial "${BETA}:18080" >$WORK/dial-beta.log 2>&1
echo "beta dial exit=$? out=$(cat $WORK/dial-beta.log)"
sleep 0.5
echo "--- echo log ---"
cat $WORK/echo.log
echo "--- singbox log (tail) ---"
tail -n 30 $WORK/singbox.log
kill $SB_PID 2>/dev/null
kill $ECHO_PID 2>/dev/null
sleep 0.3
INNER
chmod +x $WORK/run.sh

echo "=== run inside the namespace ==="
ip netns exec $NAMESPACE bash $WORK/run.sh
echo "=== namespace cleanup ==="
ip netns del $NAMESPACE 2>/dev/null
ip link show jjbq01 2>/dev/null || echo "no leftover tun in the host namespace"
echo "=== done ==="
