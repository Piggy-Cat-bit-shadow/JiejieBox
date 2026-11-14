package naive

import "testing"

// TestShouldWarnUnsetCongestionControl pins the precision of the outbound warning.
//
// The warning is about a QUIC choice. An HTTP/2-only outbound (quic: false) never
// reads quic_congestion_control, so warning there would be noise about a knob that
// does nothing. An explicitly configured algorithm is not a default either, so it
// must stay quiet regardless of the transport.
func TestShouldWarnUnsetCongestionControl(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		quic  bool
		value string
		want  bool
	}{
		{name: "quic unset warns", quic: true, value: "", want: true},
		{name: "quic explicit bbr is quiet", quic: true, value: "bbr", want: false},
		{name: "quic explicit cubic is quiet", quic: true, value: "cubic", want: false},
		{name: "h2 unset is quiet", quic: false, value: "", want: false},
		{name: "h2 explicit is quiet", quic: false, value: "bbr", want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := shouldWarnUnsetCongestionControl(testCase.quic, testCase.value)
			if got != testCase.want {
				t.Fatalf("shouldWarnUnsetCongestionControl(%v, %q) = %v, want %v",
					testCase.quic, testCase.value, got, testCase.want)
			}
		})
	}
}
