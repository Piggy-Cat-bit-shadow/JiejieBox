//go:build with_utls

package tls

import (
	"context"
	"net"
	"testing"

	tf "github.com/sagernet/sing-box/common/tlsfragment"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	utls "github.com/metacubex/utls"
	"github.com/stretchr/testify/require"
)

// Guards the wrap gate in UTLSClientConfig.Client(): tf.Conn must wrap the
// underlying connection whenever either `fragment` or `record_fragment` is
// set. Mirrors the STDClientConfig gate tests to keep both code paths in
// lockstep.

func newUTLSClientConfigForGateTest(fragment, recordFragment bool) *UTLSClientConfig {
	return &UTLSClientConfig{
		ctx:            context.Background(),
		config:         &utls.Config{ServerName: "example.com", InsecureSkipVerify: true},
		id:             utls.HelloChrome_Auto,
		fragment:       fragment,
		recordFragment: recordFragment,
	}
}

func TestUTLSClient_Client_NoFragment_DoesNotWrap(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	wrapped, err := newUTLSClientConfigForGateTest(false, false).Client(client)
	require.NoError(t, err)
	_, isTF := wrapped.NetConn().(*tf.Conn)
	require.False(t, isTF, "no fragment flags: must not wrap with tf.Conn")
}

func TestUTLSClient_Client_FragmentOnly_Wraps(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	wrapped, err := newUTLSClientConfigForGateTest(true, false).Client(client)
	require.NoError(t, err)
	_, isTF := wrapped.NetConn().(*tf.Conn)
	require.True(t, isTF, "fragment=true: must wrap with tf.Conn")
}

func TestUTLSClient_Client_RecordFragmentOnly_Wraps(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	wrapped, err := newUTLSClientConfigForGateTest(false, true).Client(client)
	require.NoError(t, err)
	_, isTF := wrapped.NetConn().(*tf.Conn)
	require.True(t, isTF, "record_fragment=true: must wrap with tf.Conn")
}

func TestUTLSClient_Client_BothFragment_Wraps(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	wrapped, err := newUTLSClientConfigForGateTest(true, true).Client(client)
	require.NoError(t, err)
	_, isTF := wrapped.NetConn().(*tf.Conn)
	require.True(t, isTF, "both fragment flags: must wrap with tf.Conn")
}

func TestUTLSFingerprintSupportsECH(t *testing.T) {
	t.Parallel()

	for _, fingerprint := range []string{
		"", "chrome", "chrome_psk", "chrome_psk_shuffle", "chrome_padding_psk_shuffle",
		"chrome_pq", "chrome_pq_psk", "firefox",
	} {
		require.True(t, utlsFingerprintSupportsECH(fingerprint),
			"%q must be accepted with ECH", fingerprint)
	}
	for _, fingerprint := range []string{
		"safari", "edge", "ios", "android", "360", "qq", "random", "randomized",
	} {
		require.False(t, utlsFingerprintSupportsECH(fingerprint),
			"%q has no ECH-capable extension and must be refused", fingerprint)
	}
}

func TestNewUTLSClientRejectsECHOnUnsupportedFingerprint(t *testing.T) {
	t.Parallel()

	_, err := newUTLSClient(context.Background(), log.NewNOPFactory().Logger(), "example.com", option.OutboundTLSOptions{
		Enabled: true,
		UTLS: &option.OutboundUTLSOptions{
			Enabled:     true,
			Fingerprint: "safari",
		},
		ECH: &option.OutboundECHOptions{Enabled: true},
	}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not support ECH")
}
