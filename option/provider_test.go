package option_test

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

func decodeOptions(t *testing.T, content string) (option.Options, error) {
	t.Helper()
	return json.UnmarshalExtendedContext[option.Options](include.Context(context.Background()), []byte(content))
}

// TestProviderConfigurationValidation is the configuration half of the provider audit: every
// invalid declaration must be refused at decode/check time with a message that names the provider,
// not discovered later by the refresh worker.
func TestProviderConfigurationValidation(t *testing.T) {
	t.Parallel()
	valid := `{"providers": [{"type": "remote", "tag": "air", "url": "https://example.com/sub"}]}`
	options, err := decodeOptions(t, valid)
	require.NoError(t, err)
	require.Len(t, options.Providers, 1)
	require.Equal(t, "air", options.Providers[0].Tag)
	remoteOptions, ok := options.Providers[0].Options.(*option.ProviderRemoteOptions)
	require.True(t, ok)
	require.Equal(t, "https://example.com/sub", remoteOptions.URL)

	cases := []struct {
		name    string
		content string
		message string
	}{
		{
			name:    "missing type",
			content: `{"providers": [{"tag": "air", "url": "https://example.com/sub"}]}`,
			message: "unknown provider type",
		},
		{
			name:    "unknown field",
			content: `{"providers": [{"type": "remote", "tag": "air", "url": "https://example.com/sub", "surprise": true}]}`,
			message: "surprise",
		},
		{
			name:    "invalid include regexp",
			content: `{"providers": [{"type": "remote", "tag": "air", "url": "https://example.com/sub", "include": "("}]}`,
			message: "error parsing regexp",
		},
		{
			name:    "path and initial_path",
			content: `{"providers": [{"type": "remote", "tag": "air", "url": "https://example.com/sub", "path": "a.json", "initial_path": "b.json"}]}`,
			message: "mutually exclusive",
		},
		{
			name:    "invalid update interval",
			content: `{"providers": [{"type": "remote", "tag": "air", "url": "https://example.com/sub", "update_interval": "soon"}]}`,
			message: "update_interval",
		},
		{
			name:    "duplicate tag",
			content: `{"providers": [{"type": "inline", "tag": "air", "outbounds": []}, {"type": "inline", "tag": "air", "outbounds": []}]}`,
			message: "duplicate provider tag",
		},
		{
			name:    "tag with slash",
			content: `{"providers": [{"type": "inline", "tag": "a/b", "outbounds": []}]}`,
			message: "must not contain '/'",
		},
		// TLS/AnyTLS overrides and inline endpoints are NOT implemented in this stage. Their fields
		// do not exist, so a document using them is refused with an unknown-field error instead of
		// being accepted and silently ignored.
		{
			name:    "unimplemented override_tls",
			content: `{"providers": [{"type": "remote", "tag": "air", "url": "https://example.com/sub", "override_tls": {"server_name": "x"}}]}`,
			message: "override_tls",
		},
		{
			name:    "unimplemented override_anytls",
			content: `{"providers": [{"type": "remote", "tag": "air", "url": "https://example.com/sub", "override_anytls": {"client_metadata": "x"}}]}`,
			message: "override_anytls",
		},
		{
			name:    "unimplemented inline endpoints",
			content: `{"providers": [{"type": "inline", "tag": "air", "endpoints": [{"type": "wireguard", "tag": "wg"}]}]}`,
			message: "endpoints",
		},
		// Missing URL, an unsupported URL scheme and a local provider without a path are refused by
		// the provider constructors (see provider/remote and provider/local), which is where the
		// concrete type is known. They are covered there instead of here.
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeOptions(t, testCase.content)
			require.Error(t, err)
			require.ErrorContains(t, err, testCase.message)
		})
	}
}

// TestProviderConfigurationKeepsOldConfigsUntouched proves that the field is additive: a
// configuration without `providers` decodes exactly as before, and an empty or null array is legal.
func TestProviderConfigurationKeepsOldConfigsUntouched(t *testing.T) {
	t.Parallel()
	for _, content := range []string{
		`{"log": {"disabled": true}}`,
		`{"providers": []}`,
		`{"providers": null}`,
		`{"providers": [{"type": "inline", "tag": "air", "outbounds": null}]}`,
		`{"providers": [{"type": "remote", "tag": "air", "url": "https://example.com", "include": null, "exclude": null, "http_client": null}]}`,
	} {
		_, err := decodeOptions(t, content)
		require.NoError(t, err, content)
	}
}
