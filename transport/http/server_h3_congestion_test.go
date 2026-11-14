//go:build with_quic

package http

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/congestion"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// TestResolveServerCongestionControl pins the resolution of the two server-side
// knobs before any connection exists:
//   - bbr_profile keeps its exact meaning and wins only because naming both is
//     refused at option load;
//   - quic_congestion_control: "bbr" produces the same sender as
//     bbr_profile: "standard";
//   - unset and "default" keep quic-go's own sender (this inbound's documented
//     default), and unknown names are refused.
func TestResolveServerCongestionControl(t *testing.T) {
	standard := option.ServerBBRProfile{Name: option.BBRProfileStandard}

	for _, options := range []option.QUICOptions{
		{},
		{CongestionControl: ""},
		{CongestionControl: "default"},
	} {
		factory, err := resolveServerCongestionControl(options)
		require.NoError(t, err)
		require.Nil(t, factory, "an unset/default algorithm must keep the library sender")
	}

	bbrFromProfile, err := resolveServerCongestionControl(option.QUICOptions{BBRProfile: standard})
	require.NoError(t, err)
	require.NotNil(t, bbrFromProfile)

	bbrFromName, err := resolveServerCongestionControl(option.QUICOptions{CongestionControl: "bbr"})
	require.NoError(t, err)
	require.NotNil(t, bbrFromName)
	require.Equal(t, reflect.ValueOf(bbrFromProfile).Pointer(), reflect.ValueOf(bbrFromName).Pointer(),
		`quic_congestion_control "bbr" must select the same sender as bbr_profile "standard"`)

	cubic, err := resolveServerCongestionControl(option.QUICOptions{CongestionControl: "cubic"})
	require.NoError(t, err)
	require.NotNil(t, cubic)
	require.NotEqual(t, reflect.ValueOf(bbrFromProfile).Pointer(), reflect.ValueOf(cubic).Pointer())

	reno, err := resolveServerCongestionControl(option.QUICOptions{CongestionControl: "reno"})
	require.NoError(t, err)
	require.NotNil(t, reno)
	require.NotEqual(t, reflect.ValueOf(cubic).Pointer(), reflect.ValueOf(reno).Pointer(),
		"cubic and reno must not resolve to the same factory")

	_, err = resolveServerCongestionControl(option.QUICOptions{
		BBRProfile:        standard,
		CongestionControl: "cubic",
	})
	require.Error(t, err, "naming both knobs must be refused, not silently resolved")
	require.Contains(t, err.Error(), "mutually exclusive")

	_, err = resolveServerCongestionControl(option.QUICOptions{CongestionControl: "BBR"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown quic_congestion_control")
}

// TestServerCongestionControlIsAppliedOnHandshake drives the PRODUCTION
// ConfigureHTTP3ListenerFunc and a real QUIC handshake, then inspects the sender
// the server installed. This is the runtime assertion the resolver test cannot
// make: it proves the configured algorithm reaches conn.SetCongestionControl.
//
// A *quic.Conn exposes no getter, so the seam is applyServerCongestionControl,
// which production assigns to SetCongestionControl and this test replaces with a
// recorder.
func TestServerCongestionControlIsAppliedOnHandshake(t *testing.T) {
	cases := []struct {
		name    string
		options option.QUICOptions
		want    string
	}{
		{
			name:    "unset keeps the library default",
			options: option.QUICOptions{},
			want:    "",
		},
		{
			name:    "default keeps the library default",
			options: option.QUICOptions{CongestionControl: "default"},
			want:    "",
		},
		{
			name:    "explicit cubic",
			options: option.QUICOptions{CongestionControl: "cubic"},
			want:    "*congestion.cubicSender",
		},
		{
			name:    "explicit bbr",
			options: option.QUICOptions{CongestionControl: "bbr"},
			want:    "*congestion.bbrSender",
		},
		{
			name:    "explicit reno",
			options: option.QUICOptions{CongestionControl: "reno"},
			want:    "*congestion.cubicSender",
		},
		{
			name:    "bbr_profile standard still selects BBR",
			options: option.QUICOptions{BBRProfile: option.ServerBBRProfile{Name: option.BBRProfileStandard}},
			want:    "*congestion.bbrSender",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			applied := make(chan congestion.CongestionControl, 1)
			previous := applyServerCongestionControl
			applyServerCongestionControl = func(_ *quic.Conn, sender congestion.CongestionControl) {
				select {
				case applied <- sender:
				default:
				}
			}
			t.Cleanup(func() { applyServerCongestionControl = previous })

			_, address := startRealH3ListenerWithOptions(t, http.HandlerFunc(defaultH3Handler), testCase.options)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			conn, err := quic.DialAddr(ctx, address, testClientTLSConfig(), &quic.Config{})
			require.NoError(t, err)
			defer conn.CloseWithError(0, "")

			if testCase.want == "" {
				select {
				case sender := <-applied:
					t.Fatalf("an unset algorithm must keep the library sender, got %T", sender)
				case <-time.After(500 * time.Millisecond):
				}
				return
			}
			select {
			case sender := <-applied:
				require.Equal(t, testCase.want, reflect.TypeOf(sender).String())
			case <-time.After(5 * time.Second):
				t.Fatal("the configured congestion control never reached the connection")
			}
		})
	}
}
