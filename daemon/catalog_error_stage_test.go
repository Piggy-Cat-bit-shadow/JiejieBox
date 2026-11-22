package daemon

// P03 at the product entry: shapes the catalog refuses must reach the SAME eager constructor error
// at start, not start successfully and fail on the first dial. These are two of the preflight cases
// the catalog eligibility deliberately refuses (http.go catalogEligible).

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func catalogErrorProfile(outbound string) string {
	return fmt.Sprintf(`{
  "log": {"disabled": true},
  "outbounds": [%s, {"type": "direct", "tag": "direct"}],
  "route": {"final": "direct"}
}`, outbound)
}

func TestNodeCatalogRefusedShapesKeepTheirEagerErrorStage(t *testing.T) {
	cases := []struct {
		name     string
		outbound string
		want     string
	}{
		{
			name: "http path and host",
			outbound: `{"type": "http", "tag": "bad", "server": "192.0.2.1", "server_port": 8080,
				"version": 1, "path": "/", "headers": {"Host": ["example.com"]}}`,
			want: "Host header and path are not allowed at the same time",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			ctx := crossSystemContext(t, directory, nil)
			service := NewStartedService(ServiceOptions{Context: ctx})
			t.Cleanup(func() {
				_ = service.CloseService()
				service.Close()
			})
			err := service.StartOrReloadService(t.Context(), catalogErrorProfile(testCase.outbound), nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), testCase.want,
				"the eager constructor's error must be preserved, not deferred to the first dial")
		})
	}
}
