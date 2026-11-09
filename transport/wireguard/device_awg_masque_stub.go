//go:build with_awg

package wireguard

import (
	"github.com/sagernet/sing-box/option"
)

// The QUIC/MASQUE-flavoured junk-packet generators (I1/I2 and the dynamic mode) belong to the
// LX masquerade feature set and are not part of this port: explicit i1..i5 strings still render
// through awgIpcLines, and an unset generator simply produces nothing.
func masqueI1I2(_ option.AmneziaWGOptions) (string, string, error) { return "", "", nil }

func masqueQUICDynamic(_ option.AmneziaWGOptions) bool { return false }
