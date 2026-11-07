package provider

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

func TestProviderLocalRequiresPath(t *testing.T) {
	t.Parallel()
	_, err := NewProviderLocal(context.Background(), nil, log.NewNOPFactory(), "p", option.ProviderLocalOptions{})
	require.ErrorContains(t, err, "path is required")
}

func TestProviderLocalRejectsDirectory(t *testing.T) {
	t.Parallel()
	_, err := NewProviderLocal(context.Background(), nil, log.NewNOPFactory(), "p", option.ProviderLocalOptions{Path: t.TempDir()})
	require.ErrorContains(t, err, "is a directory")
}
