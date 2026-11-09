package process

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

type fakeProcessSearcher struct {
	fullCalls  int
	ownerCalls int
}

func (s *fakeProcessSearcher) FindProcessInfo(context.Context, string, netip.AddrPort, netip.AddrPort) (*adapter.ConnectionOwner, error) {
	s.fullCalls++
	return &adapter.ConnectionOwner{UserId: 7}, nil
}

func (s *fakeProcessSearcher) ResetCache() {}

func (s *fakeProcessSearcher) Close() error { return nil }

type fakeOwnerSearcher struct {
	fakeProcessSearcher
}

func (s *fakeOwnerSearcher) FindProcessOwner(context.Context, string, netip.AddrPort, netip.AddrPort) (*adapter.ConnectionOwner, error) {
	s.ownerCalls++
	return &adapter.ConnectionOwner{UserId: 9}, nil
}

func TestFindProcessOwnerUsesTheOwnerPath(t *testing.T) {
	t.Parallel()
	searcher := &fakeOwnerSearcher{}
	info, err := FindProcessOwner(searcher, context.Background(), "tcp", netip.AddrPort{}, netip.AddrPort{})
	require.NoError(t, err)
	require.Equal(t, int32(9), info.UserId)
	require.Equal(t, 1, searcher.ownerCalls)
	require.Zero(t, searcher.fullCalls, "the full lookup must not run when the owner path exists")
}

func TestFindProcessOwnerFallsBackWhenUnsupported(t *testing.T) {
	t.Parallel()
	searcher := &fakeProcessSearcher{}
	info, err := FindProcessOwner(searcher, context.Background(), "tcp", netip.AddrPort{}, netip.AddrPort{})
	require.NoError(t, err)
	require.Equal(t, int32(7), info.UserId)
	require.Equal(t, 1, searcher.fullCalls)
}
