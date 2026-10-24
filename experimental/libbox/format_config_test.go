package libbox

import (
	"strings"
	"testing"
)

// FormatConfig is the daemon-side `format` path: parse the user's config, then encode it back.
// It must preserve real ports/protocol fields and omit zero-valued ones. The same defect class
// as the option-level test, but through the actual command implementation.
func TestFormatConfigPreservesProtocolFields(t *testing.T) {
	input := `{
  "outbounds": [
    {
      "type": "vless",
      "tag": "a",
      "server": "example.com",
      "server_port": 443,
      "uuid": "00000000-0000-0000-0000-000000000000"
    },
    {
      "type": "vmess",
      "tag": "b",
      "server": "example.com",
      "server_port": 8443,
      "uuid": "00000000-0000-0000-0000-000000000000"
    }
  ]
}`
	formatted, err := FormatConfig(input)
	if err != nil {
		t.Fatalf("FormatConfig: %v", err)
	}
	if !strings.Contains(formatted.Value, `"server_port": 443`) {
		t.Fatalf("format dropped the VLESS server_port:\n%s", formatted.Value)
	}
	if !strings.Contains(formatted.Value, `"server_port": 8443`) {
		t.Fatalf("format dropped the VMess server_port:\n%s", formatted.Value)
	}
	if strings.Contains(formatted.Value, `"server_port": 0`) {
		t.Fatalf("format wrote a zero server_port:\n%s", formatted.Value)
	}
	if strings.Contains(formatted.Value, `"security"`) {
		t.Fatalf("format wrote an empty VMess security:\n%s", formatted.Value)
	}
}
