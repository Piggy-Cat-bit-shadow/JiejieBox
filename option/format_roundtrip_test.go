package option

import (
	"encoding/json"
	"testing"
)

// format must not invent zero-valued fields, and it must not lose a field that carries a real
// port/protocol choice. These are the shapes the formatter writes back to the user; a dropped
// server_port or security is a broken config, not cosmetic output.
func TestFormatRoundTripKeepsProtocolFields(t *testing.T) {
	vless := VLESSOutboundOptions{
		ServerOptions: ServerOptions{Server: "example.com", ServerPort: 443},
		UUID:          "00000000-0000-0000-0000-000000000000",
	}
	encoded, err := json.Marshal(vless)
	if err != nil {
		t.Fatalf("marshal VLESS: %v", err)
	}
	var vlessFields map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &vlessFields); err != nil {
		t.Fatalf("decode VLESS: %v", err)
	}
	if string(vlessFields["server_port"]) != "443" {
		t.Fatalf("VLESS server_port = %s, want 443; output: %s", vlessFields["server_port"], encoded)
	}

	var decoded VLESSOutboundOptions
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("round-trip VLESS: %v", err)
	}
	if decoded.Server != "example.com" || decoded.ServerPort != 443 || decoded.UUID != vless.UUID {
		t.Fatalf("VLESS round-trip changed fields: %+v", decoded)
	}

	zeroPort, err := json.Marshal(VLESSOutboundOptions{
		ServerOptions: ServerOptions{Server: "example.com"},
		UUID:          vless.UUID,
	})
	if err != nil {
		t.Fatalf("marshal zero-port VLESS: %v", err)
	}
	var zeroFields map[string]json.RawMessage
	if err = json.Unmarshal(zeroPort, &zeroFields); err != nil {
		t.Fatalf("decode zero-port VLESS: %v", err)
	}
	if _, present := zeroFields["server_port"]; present {
		t.Fatalf("a zero server_port must be omitted, not written as 0: %s", zeroPort)
	}

	vmess, err := json.Marshal(VMessOutboundOptions{
		ServerOptions: ServerOptions{Server: "example.com", ServerPort: 8443},
		UUID:          vless.UUID,
	})
	if err != nil {
		t.Fatalf("marshal VMess: %v", err)
	}
	var vmessFields map[string]json.RawMessage
	if err = json.Unmarshal(vmess, &vmessFields); err != nil {
		t.Fatalf("decode VMess: %v", err)
	}
	if string(vmessFields["server_port"]) != "8443" {
		t.Fatalf("VMess server_port = %s, want 8443", vmessFields["server_port"])
	}
	if _, present := vmessFields["security"]; present {
		t.Fatalf("an empty security must be omitted, not written as an empty string: %s", vmess)
	}
	withSecurity, err := json.Marshal(VMessOutboundOptions{
		ServerOptions: ServerOptions{Server: "example.com", ServerPort: 8443},
		UUID:          vless.UUID,
		Security:      "none",
	})
	if err != nil {
		t.Fatalf("marshal VMess security: %v", err)
	}
	if err = json.Unmarshal(withSecurity, &vmessFields); err != nil {
		t.Fatalf("decode VMess security: %v", err)
	}
	if string(vmessFields["security"]) != `"none"` {
		t.Fatalf("VMess security = %s, want \"none\"", vmessFields["security"])
	}
}

func TestFormatOmitsZeroOpenVPNPriority(t *testing.T) {
	encoded, err := json.Marshal(OpenVPNPushDNSServerOptions{})
	if err != nil {
		t.Fatalf("marshal OpenVPN push options: %v", err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode OpenVPN push options: %v", err)
	}
	if _, present := fields["priority"]; present {
		t.Fatalf("a zero priority must be omitted: %s", encoded)
	}
}

func TestFormatDERPSTUNPortRoundTrip(t *testing.T) {
	options := DERPSTUNListenOptions{
		Enabled:       true,
		ListenOptions: ListenOptions{ListenPort: 3478},
	}
	encoded, err := json.Marshal(options)
	if err != nil {
		t.Fatalf("marshal DERP STUN: %v", err)
	}
	if string(encoded) != "3478" {
		t.Fatalf("DERP STUN = %s, want the compact port form 3478", encoded)
	}
	var decoded DERPSTUNListenOptions
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("round-trip DERP STUN: %v", err)
	}
	if !decoded.Enabled || decoded.ListenPort != 3478 {
		t.Fatalf("DERP STUN round-trip changed fields: %+v", decoded)
	}
}

// Removing the direct action must make it a loud config error, not leave a ghost action that
// parses, reports success and then does nothing at runtime.
func TestDirectRuleActionIsRejected(t *testing.T) {
	var action RuleAction
	err := json.Unmarshal([]byte(`{"action":"direct"}`), &action)
	if err == nil {
		t.Fatal("the removed direct action still parses")
	}
}
