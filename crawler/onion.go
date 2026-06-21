package crawler

import (
	"net"
	"strings"

	"github.com/decred/dcrd/wire"
)

// onionV3HostLen is the length of the base32-encoded label that precedes the
// ".onion" suffix in a Tor v3 address: 32-byte ed25519 public key + 2-byte
// checksum + 1 version byte = 35 bytes, which base32-encodes to 56 characters.
const onionV3HostLen = 56

// isOnionV3 reports whether host looks like a Tor v3 onion address
// (56 base32 characters followed by ".onion"). It validates the shape only, not
// the embedded checksum; that stricter check belongs with the addrv2 work once
// we parse wire.NetAddressV2 TorV3 entries.
func isOnionV3(host string) bool {
	host = strings.ToLower(host)
	label, ok := strings.CutSuffix(host, ".onion")
	if !ok || len(label) != onionV3HostLen {
		return false
	}
	for _, r := range label {
		if !isBase32Lower(r) {
			return false
		}
	}
	return true
}

// isBase32Lower reports whether r is in the lowercased base32 alphabet (a-z and
// 2-7) used to encode v3 onion addresses.
func isBase32Lower(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '2' && r <= '7')
}

// onionHostToNetAddress is the peer.Config.HostToNetAddress hook used for onion
// dials. A v3 onion address cannot be represented in a 16-byte wire.NetAddress,
// but the peer only uses this value for the (cosmetic) addrYou field of the
// version message, so a zero placeholder is enough to complete the handshake.
func onionHostToNetAddress(_ string, port uint16, services wire.ServiceFlag) (*wire.NetAddress, error) {
	return wire.NewNetAddressIPPort(net.IPv4zero, port, services), nil
}
