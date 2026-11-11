#!/usr/bin/env bash
# netlab.sh - run a command inside a private network namespace with a shaped
# veth link.
#
# # Why this shape
#
# The laboratory must add latency, loss and bandwidth limits without touching a
# host interface, without root, and without leaving anything behind when the
# command crashes. A user + network namespace gives all three: inside it we are
# uid 0, the only interfaces that exist are loopback and the veth pair this
# script creates, and the namespace is destroyed when the process exits - so
# addresses and qdiscs cannot outlive the run.
#
# Both ends of the veth live in the same namespace. A packet from 10.213.0.1 to
# 10.213.0.2 leaves one end and arrives on the other, so `tc netem` on each
# end's egress shapes one direction of the path. One namespace, one veth, two
# directions shaped independently.
#
# # What it never does
#
# It never names an interface it did not create, and every `ip`/`tc` call runs
# inside the new namespace. It does not use sudo and does not need root.
#
# # Usage
#
#   netlab.sh --profile remote -- perflab run ...
#   netlab.sh --rtt-ms 80 --loss-pct 1 --bandwidth-mbps 100 -- perflab run ...
#   netlab.sh --profile normal --dry-run -- perflab run ...
#   netlab.sh --profile loopback -- perflab run ...    # no namespace, no shaping
#
# Profiles (RTT is round trip; each direction gets half):
#   loopback  0 ms   0 %     normal  20 ms  0 %
#   remote    80 ms  1 %     weak    160 ms 3 %
#
# Exported to the command:
#   PERF_SERVER_ADDR / PERF_CLIENT_ADDR / PERF_SITE_ADDR / PERF_WIRE_IFACE
#   PERF_NET_TOPOLOGY, PERF_NET_PROFILE, PERF_NET_RTT_MS, PERF_NET_DELAY_MS,
#   PERF_NET_JITTER_MS, PERF_NET_LOSS_PCT, PERF_NET_BANDWIDTH_MBPS,
#   PERF_NET_MTU, PERF_NET_QDISC, PERF_NET_NAMESPACE
set -euo pipefail

prefix="plab"
profile=""
rtt_ms=""
jitter_ms="0"
loss_pct=""
bandwidth_mbps=""
mtu="1500"
dry_run=0
command=()

usage() {
  sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --profile) profile="$2"; shift 2 ;;
    --rtt-ms) rtt_ms="$2"; shift 2 ;;
    --jitter-ms) jitter_ms="$2"; shift 2 ;;
    --loss-pct) loss_pct="$2"; shift 2 ;;
    --bandwidth-mbps) bandwidth_mbps="$2"; shift 2 ;;
    --mtu) mtu="$2"; shift 2 ;;
    --prefix) prefix="$2"; shift 2 ;;
    --dry-run) dry_run=1; shift ;;
    --) shift; command=("$@"); break ;;
    -h|--help) usage ;;
    *) echo "netlab: unknown argument $1" >&2; usage ;;
  esac
done

if [ ${#command[@]} -eq 0 ]; then
  echo "netlab: a command after -- is required" >&2
  usage
fi

case "$profile" in
  "") ;;
  loopback) rtt_ms="${rtt_ms:-0}"; loss_pct="${loss_pct:-0}" ;;
  normal) rtt_ms="${rtt_ms:-20}"; loss_pct="${loss_pct:-0}" ;;
  remote) rtt_ms="${rtt_ms:-80}"; loss_pct="${loss_pct:-1}" ;;
  weak) rtt_ms="${rtt_ms:-160}"; loss_pct="${loss_pct:-3}" ;;
  *) echo "netlab: unknown profile $profile (loopback|normal|remote|weak)" >&2; exit 2 ;;
esac
rtt_ms="${rtt_ms:-0}"
loss_pct="${loss_pct:-0}"
jitter_ms="${jitter_ms:-0}"

unshaped=0
if [ "$profile" = "loopback" ]; then
  unshaped=1
fi
if [ "$rtt_ms" = "0" ] && [ "$loss_pct" = "0" ] && [ -z "$bandwidth_mbps" ]; then
  unshaped=1
fi

if [ "$unshaped" = "1" ]; then
  if [ "$dry_run" = "1" ]; then
    echo "# dry-run: unshaped run, no namespace"
    printf 'PERF_NET_TOPOLOGY=loopback PERF_NET_PROFILE=%s %s\n' "${profile:-loopback}" "${command[*]}"
    exit 0
  fi
  PERF_NET_TOPOLOGY=loopback \
  PERF_NET_PROFILE="${profile:-loopback}" \
  PERF_NET_RTT_MS="$rtt_ms" \
  PERF_NET_DELAY_MS="0" \
  PERF_NET_JITTER_MS="$jitter_ms" \
  PERF_NET_LOSS_PCT="$loss_pct" \
  exec "${command[@]}"
fi

one_way=$(awk -v rtt="$rtt_ms" 'BEGIN { printf "%.3f", rtt / 2 }')
# An explicit --rtt-ms/--loss-pct run still needs a label that distinguishes it
# from another explicit run: without one, two loss levels collapse into the same
# "custom" cell and a summary that keys on the profile would merge them.
if [ -z "$profile" ]; then
  label="custom-rtt${rtt_ms}-loss${loss_pct}"
else
  label="$profile"
fi
netem_args="delay ${one_way}ms"
if awk -v j="$jitter_ms" 'BEGIN { exit !(j > 0) }'; then
  netem_args="$netem_args ${jitter_ms}ms"
fi
if awk -v l="$loss_pct" 'BEGIN { exit !(l > 0) }'; then
  netem_args="$netem_args loss ${loss_pct}%"
fi

tag="${prefix}-$$"
tag="${tag:0:11}"
client_if="${tag}a"
server_if="${tag}b"
client_ip="10.213.0.1"
server_ip="10.213.0.2"
namespace="perf-${tag}"

if [ "$dry_run" = "1" ]; then
  echo "# dry-run: would run inside a new user+network namespace"
  echo "# namespaces: client ($namespace) and server (client + 1), joined by one veth"
  echo "# veth:       $client_if ($client_ip) <-> $server_if ($server_ip), mtu $mtu"
  if [ -n "$bandwidth_mbps" ]; then
    echo "# qdisc:      htb rate ${bandwidth_mbps}mbit + netem $netem_args, per direction"
  else
    echo "# qdisc:      netem $netem_args, per direction"
  fi
  echo "# command:    ${command[*]}"
  exit 0
fi

inner_script="$(mktemp -t netlab-XXXXXX.sh)"
trap 'rm -f "$inner_script"' EXIT
cat > "$inner_script" <<'INNER'
set -euo pipefail
# The server side needs its own network namespace. If both addresses were local
# to one namespace the kernel would short-circuit them through loopback and no
# qdisc would ever see the traffic - a shaped run that measures an unshaped
# path.
ip link set lo up
unshare -n -- sleep 3600 &
server_ns=$!
cleanup() {
  kill "$server_ns" 2>/dev/null || true
  wait "$server_ns" 2>/dev/null || true
}
trap cleanup EXIT

ip link add "$NL_CLIENT_IF" type veth peer name "$NL_SERVER_IF"
ip link set "$NL_SERVER_IF" netns "$server_ns"
ip addr add "$NL_CLIENT_IP/24" dev "$NL_CLIENT_IF"
ip link set "$NL_CLIENT_IF" mtu "$NL_MTU" up
ip link set "$NL_CLIENT_IF" txqueuelen 1000

nsenter -t "$server_ns" -n -- bash -c "
  set -e
  ip link set lo up
  ip addr add $NL_SERVER_IP/24 dev $NL_SERVER_IF
  ip link set $NL_SERVER_IF mtu $NL_MTU up
  ip link set $NL_SERVER_IF txqueuelen 1000
"

qdisc_record=""
for dev in "$NL_CLIENT_IF" "$NL_SERVER_IF"; do
  if [ -n "$NL_BANDWIDTH" ]; then
    if [ "$dev" = "$NL_SERVER_IF" ]; then
      nsenter -t "$server_ns" -n -- tc qdisc add dev "$dev" root handle 1: htb default 1
      nsenter -t "$server_ns" -n -- tc class add dev "$dev" parent 1: classid 1:1 htb rate "${NL_BANDWIDTH}mbit" burst 64k cburst 64k
      nsenter -t "$server_ns" -n -- tc qdisc add dev "$dev" parent 1:1 handle 10: netem $NL_NETEM
    else
      tc qdisc add dev "$dev" root handle 1: htb default 1
      tc class add dev "$dev" parent 1: classid 1:1 htb rate "${NL_BANDWIDTH}mbit" burst 64k cburst 64k
      tc qdisc add dev "$dev" parent 1:1 handle 10: netem $NL_NETEM
    fi
    qdisc_record="${qdisc_record}${dev}|htb|root|rate ${NL_BANDWIDTH}mbit;${dev}|netem|1:1|${NL_NETEM};"
  else
    if [ "$dev" = "$NL_SERVER_IF" ]; then
      nsenter -t "$server_ns" -n -- tc qdisc add dev "$dev" root netem $NL_NETEM
    else
      tc qdisc add dev "$dev" root netem $NL_NETEM
    fi
    qdisc_record="${qdisc_record}${dev}|netem|root|${NL_NETEM};"
  fi
done

export PERF_SERVER_ADDR="$NL_SERVER_IP"
export PERF_CLIENT_ADDR="$NL_CLIENT_IP"
export PERF_SITE_ADDR="$NL_SERVER_IP"
export PERF_WIRE_IFACE="$NL_CLIENT_IF"
export PERF_SERVER_NS="$server_ns"
export PERF_NET_TOPOLOGY=netns-veth
export PERF_NET_PROFILE="$NL_PROFILE"
export PERF_NET_RTT_MS="$NL_RTT"
export PERF_NET_DELAY_MS="$NL_DELAY"
export PERF_NET_JITTER_MS="$NL_JITTER"
export PERF_NET_LOSS_PCT="$NL_LOSS"
export PERF_NET_BANDWIDTH_MBPS="$NL_BANDWIDTH"
export PERF_NET_MTU="$NL_MTU"
export PERF_NET_QDISC="$qdisc_record"
export PERF_NET_NAMESPACE="$NL_NAMESPACE"

# Prove the shaping is in force before the measurement starts. An unshaped
# "remote" cell is worse than no cell, so this is printed here and recorded in
# the run as well.
tc qdisc show dev "$NL_CLIENT_IF" >&2
nsenter -t "$server_ns" -n -- tc qdisc show dev "$NL_SERVER_IF" >&2

# Run the command in the client namespace, then take the holder down with it.
status=0
"$@" || status=$?
exit "$status"
INNER
chmod +x "$inner_script"

export NL_CLIENT_IF="$client_if"
export NL_SERVER_IF="$server_if"
export NL_CLIENT_IP="$client_ip"
export NL_SERVER_IP="$server_ip"
export NL_MTU="$mtu"
export NL_NETEM="$netem_args"
export NL_BANDWIDTH="$bandwidth_mbps"
export NL_PROFILE="$label"
export NL_RTT="$rtt_ms"
export NL_DELAY="$one_way"
export NL_JITTER="$jitter_ms"
export NL_LOSS="$loss_pct"
export NL_NAMESPACE="$namespace"

exec unshare --user --map-root-user --net --mount --fork --kill-child -- bash "$inner_script" "${command[@]}"
