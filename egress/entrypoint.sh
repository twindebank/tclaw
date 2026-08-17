#!/bin/sh
# Bring up the Mullvad WireGuard tunnel, prove traffic actually leaves through
# it, then hand off to the proxy.
#
# Every failure here is fatal. A proxy running with a dead tunnel would send
# traffic straight out of Fly's own IP — the exact address Strava blocks — and
# would look healthy while doing it. Failing to start is the honest outcome.
set -eu

require() {
	eval "value=\${$1:-}"
	if [ -z "$value" ]; then
		echo "FATAL: $1 is required" >&2
		exit 1
	fi
}

require MULLVAD_WG_PRIVATE_KEY
require MULLVAD_WG_ADDRESS
require MULLVAD_RELAY_ENDPOINT
require MULLVAD_RELAY_PUBKEY

# Mullvad's own DNS inside the tunnel; prevents DNS leaking to Fly's resolver
# and resolving through a path the tunnel does not cover.
WG_DNS="${MULLVAD_DNS:-10.64.0.1}"

umask 077
mkdir -p /etc/wireguard
# Table = off stops wg-quick installing its own policy routing and kill-switch.
# That machinery needs the CONNMARK iptables extension, which Fly's kernel does
# not carry — leaving it on makes wg-quick tear the interface straight back
# down. Routing is therefore set up by hand below.
#
# AllowedIPs covers IPv4 only, on purpose: Fly's private 6PN is IPv6, and the
# flycast address plus health checks must keep using it. Upstream traffic is
# forced onto IPv4 by the proxy itself, so nothing user-facing escapes the
# tunnel via v6.
cat > /etc/wireguard/wg0.conf <<EOF
[Interface]
PrivateKey = ${MULLVAD_WG_PRIVATE_KEY}
Address = ${MULLVAD_WG_ADDRESS}
Table = off

[Peer]
PublicKey = ${MULLVAD_RELAY_PUBKEY}
Endpoint = ${MULLVAD_RELAY_ENDPOINT}
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = 25
EOF

echo "bringing up wg0 via ${MULLVAD_RELAY_ENDPOINT}"
# Fly machines may not expose the kernel WireGuard module; wg-quick falls back
# to the userspace implementation when told which one to use.
export WG_QUICK_USERSPACE_IMPLEMENTATION=wireguard-go
export WG_SUDO=1
wg-quick up wg0

# The tunnel endpoint itself must stay reachable over the physical interface,
# or sending its own handshake through the tunnel would deadlock the route.
RELAY_IP="${MULLVAD_RELAY_ENDPOINT%%:*}"
DEFAULT_GW=$(ip -4 route show default | awk '/default/ {print $3; exit}')
DEFAULT_DEV=$(ip -4 route show default | awk '/default/ {print $5; exit}')
if [ -z "$DEFAULT_GW" ] || [ -z "$DEFAULT_DEV" ]; then
	echo "FATAL: could not determine the original IPv4 default route" >&2
	exit 1
fi
echo "pinning relay ${RELAY_IP} via ${DEFAULT_GW} dev ${DEFAULT_DEV}"
ip -4 route add "${RELAY_IP}/32" via "$DEFAULT_GW" dev "$DEFAULT_DEV"

# Everything else on IPv4 goes through the tunnel.
ip -4 route replace default dev wg0

echo "verifying egress actually leaves through the tunnel"
EGRESS_IP=""
for attempt in 1 2 3 4 5 6 7 8 9 10; do
	# -4 matches how the proxy dials: the check must exercise the same path
	# the real traffic will take, or it proves nothing.
	EGRESS_IP=$(curl -4 -s --max-time 10 https://api.ipify.org || true)
	if [ -n "$EGRESS_IP" ]; then
		break
	fi
	echo "  attempt ${attempt}: no answer yet, retrying"
	sleep 3
done

if [ -z "$EGRESS_IP" ]; then
	echo "FATAL: could not determine egress IP through the tunnel" >&2
	exit 1
fi

# The whole point is that our egress is NOT Fly's. If Mullvad's address range
# is not what we see, the tunnel is not carrying traffic and we must not serve.
case "$EGRESS_IP" in
	10.*|172.1[6-9].*|172.2[0-9].*|172.3[01].*|192.168.*)
		echo "FATAL: egress IP ${EGRESS_IP} is private — tunnel is not routing" >&2
		exit 1
		;;
esac

if [ -n "${EXPECTED_EGRESS_PREFIX:-}" ]; then
	case "$EGRESS_IP" in
		"${EXPECTED_EGRESS_PREFIX}"*) ;;
		*)
			echo "FATAL: egress IP ${EGRESS_IP} does not match expected prefix ${EXPECTED_EGRESS_PREFIX}" >&2
			exit 1
			;;
	esac
fi

echo "tunnel up, egress IP ${EGRESS_IP}"
exec /usr/local/bin/egress
