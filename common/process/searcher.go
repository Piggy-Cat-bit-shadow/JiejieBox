package process

import (
	"context"
	"net/netip"
	"os/user"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
)

type Searcher interface {
	FindProcessInfo(ctx context.Context, network string, source netip.AddrPort, destination netip.AddrPort) (*adapter.ConnectionOwner, error)
	ResetCache()
	Close() error
}

// OwnerSearcher is implemented by searchers that can resolve the socket owner
// (the uid) without walking /proc for process paths or package names.
//
// A router only needs the owner when its process rules match on user/user_id
// alone; FindProcessOwner is the fast path for that case. Searchers that do not
// implement it keep the full FindProcessInfo behaviour through the fallback.
type OwnerSearcher interface {
	FindProcessOwner(ctx context.Context, network string, source netip.AddrPort, destination netip.AddrPort) (*adapter.ConnectionOwner, error)
}

var ErrNotFound = E.New("process not found")

type Config struct {
	Logger         log.ContextLogger
	PackageManager tun.PackageManager
}

func FindProcessInfo(searcher Searcher, ctx context.Context, network string, source netip.AddrPort, destination netip.AddrPort) (*adapter.ConnectionOwner, error) {
	info, err := searcher.FindProcessInfo(ctx, network, source, destination)
	if err != nil {
		return nil, err
	}
	if info.UserId != -1 && info.UserName == "" {
		osUser, _ := user.LookupId(F.ToString(info.UserId))
		if osUser != nil {
			info.UserName = osUser.Username
		}
	}
	return info, nil
}

// FindProcessOwner resolves only the socket owner. It falls back to the full
// lookup for searchers without an owner-only implementation, so callers never
// trade correctness for the fast path.
func FindProcessOwner(searcher Searcher, ctx context.Context, network string, source netip.AddrPort, destination netip.AddrPort) (*adapter.ConnectionOwner, error) {
	ownerSearcher, isOwnerSearcher := searcher.(OwnerSearcher)
	if !isOwnerSearcher {
		return FindProcessInfo(searcher, ctx, network, source, destination)
	}
	info, err := ownerSearcher.FindProcessOwner(ctx, network, source, destination)
	if err != nil {
		return nil, err
	}
	if info.UserId != -1 && info.UserName == "" {
		osUser, _ := user.LookupId(F.ToString(info.UserId))
		if osUser != nil {
			info.UserName = osUser.Username
		}
	}
	return info, nil
}
