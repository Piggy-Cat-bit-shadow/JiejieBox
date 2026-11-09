//go:build linux

package process

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/contrab/freelru"
	"github.com/sagernet/sing/contrab/maphash"
)

const (
	pathProc = "/proc"

	processPathsAllUsers = ^uint32(0)

	// processPathScanLimit bounds how many distinct UIDs may have a /proc scan
	// in flight. One UID's scan is shared by every lookup for that UID; the
	// limit only bounds different UIDs, so a burst for one app still runs one
	// scan while unrelated apps cannot each start a full walk of /proc.
	processPathScanLimit = 4
)

var _ Searcher = (*linuxSearcher)(nil)

type linuxSearcher struct {
	logger           log.ContextLogger
	packageManager   tun.PackageManager
	diagConns        [4]*socketDiagConn
	processPathCache *freelru.Cache[uint32, *uidProcessPaths]

	// scanPaths is the /proc walk. It is buildProcessPaths in production and a
	// barrier-controlled function in tests.
	scanPaths func(uid uint32) (map[uint32][]string, error)

	// cacheAccess serialises cache publication against invalidation, so a scan
	// that started before ResetCache cannot republish the snapshot it walked.
	cacheAccess sync.Mutex
	generation  atomic.Uint64

	// scanAccess guards scans. Waiter management and deletion are one critical
	// section; the scan itself and the slot wait happen outside it.
	scanAccess sync.Mutex
	scans      map[uint32]*processPathsScan
	scanSlots  chan struct{}
}

// processPathsScan is one in-flight /proc walk shared by every lookup for the
// same UID. done is closed after paths/err are published.
type processPathsScan struct {
	done  chan struct{}
	paths map[uint32][]string
	err   error
}

type uidProcessPaths struct {
	entries map[uint32][]string
}

func NewSearcher(config Config) (Searcher, error) {
	processPathCache := common.Must1(freelru.New[uint32, *uidProcessPaths](64, maphash.NewHasher[uint32]().Hash32, true))
	processPathCache.SetLifetime(time.Second)
	searcher := &linuxSearcher{
		logger:           config.Logger,
		packageManager:   config.PackageManager,
		processPathCache: processPathCache,
		scans:            make(map[uint32]*processPathsScan),
		scanSlots:        make(chan struct{}, processPathScanLimit),
	}
	for _, family := range []uint8{syscall.AF_INET, syscall.AF_INET6} {
		for _, protocol := range []uint8{syscall.IPPROTO_TCP, syscall.IPPROTO_UDP} {
			searcher.diagConns[socketDiagConnIndex(family, protocol)] = &socketDiagConn{
				family:   family,
				protocol: protocol,
				fd:       -1,
			}
		}
	}
	return searcher, nil
}

func (s *linuxSearcher) ResetCache() {
	s.invalidate()
}

// invalidate drops the cached snapshots and denies every scan already in
// flight the right to publish its result. Without the generation a scan that
// started before the reset could finish afterwards and put a snapshot taken
// under the old process table back into the cache.
func (s *linuxSearcher) invalidate() {
	s.cacheAccess.Lock()
	s.generation.Add(1)
	s.processPathCache.Purge()
	s.cacheAccess.Unlock()
}

func (s *linuxSearcher) Close() error {
	s.invalidate()
	var errs []error
	for _, conn := range s.diagConns {
		if conn == nil {
			continue
		}
		errs = append(errs, conn.Close())
	}
	return E.Errors(errs...)
}

func (s *linuxSearcher) FindProcessInfo(ctx context.Context, network string, source netip.AddrPort, destination netip.AddrPort) (*adapter.ConnectionOwner, error) {
	inode, uid, err := s.resolveSocketByNetlink(network, source, destination)
	if err != nil {
		return nil, err
	}
	processInfo := &adapter.ConnectionOwner{
		UserId: int32(uid),
	}
	processPaths, err := s.findProcessPaths(inode, uid)
	if err != nil {
		s.logger.DebugContext(ctx, "find process path: ", err)
	} else if C.IsAndroid {
		// netd chowns the DNS sockets it opens for an app to the app's uid, so the owner is
		// resolvable without the app_process wrapper. Reporting the wrapper as the process path
		// made the Android client look for a package that does not exist.
		processPaths = common.Filter(processPaths, func(path string) bool {
			return !strings.HasPrefix(path, "/system/bin/app_process")
		})
		if len(processPaths) > 0 {
			processInfo.ProcessPaths = processPaths
		}
	} else {
		processInfo.ProcessPaths = processPaths
	}
	if s.packageManager != nil {
		appID := uid % 100000
		var packageNames []string
		if sharedPackage, loaded := s.packageManager.SharedPackageByID(appID); loaded {
			packageNames = append(packageNames, sharedPackage)
		}
		if packages, loaded := s.packageManager.PackagesByID(appID); loaded {
			packageNames = append(packageNames, packages...)
		}
		processInfo.PackageNames = common.Uniq(packageNames)
	}
	return processInfo, nil
}

func (s *linuxSearcher) resolveSocketByNetlink(network string, source netip.AddrPort, destination netip.AddrPort) (inode, uid uint32, err error) {
	source = netip.AddrPortFrom(source.Addr().Unmap(), source.Port())
	destination = netip.AddrPortFrom(destination.Addr().Unmap(), destination.Port())
	family, protocol, err := socketDiagSettings(network, source)
	if err != nil {
		return 0, 0, err
	}
	conn := s.diagConns[socketDiagConnIndex(family, protocol)]
	if conn == nil {
		return 0, 0, E.New("missing socket diag connection for family=", family, " protocol=", protocol)
	}
	if destination.IsValid() && source.Addr().BitLen() == destination.Addr().BitLen() {
		inode, uid, err = conn.query(source, destination)
		if err == nil {
			return inode, uid, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return 0, 0, err
		}
	}
	return dumpSocketDiag(family, protocol, source, destination)
}

// The socket keeps the uid it was created with, while /proc reflects the
// current uid of the process, so a socket created before a privilege drop
// only appears under a scan of all users. On Android, netd chowns the DNS
// sockets it opens on behalf of an app to the app's uid.
func (s *linuxSearcher) findProcessPaths(targetInode, uid uint32) ([]string, error) {
	scanUIDs := []uint32{uid}
	if !C.IsAndroid {
		scanUIDs = append(scanUIDs, processPathsAllUsers)
	}
	for _, scanUID := range scanUIDs {
		if cached, ok := s.processPathCache.Get(scanUID); ok {
			if processPaths, found := cached.entries[targetInode]; found {
				return processPaths, nil
			}
		}
		processPaths, err := s.scanProcessPaths(scanUID)
		if err != nil {
			return nil, err
		}
		inodePaths, found := processPaths[targetInode]
		if found {
			return inodePaths, nil
		}
	}
	return nil, E.New("process of uid(", uid, "), inode(", targetInode, ") not found")
}

// scanProcessPaths returns the process table of one UID, sharing one walk
// between every concurrent lookup for that UID and bounding how many distinct
// UIDs may walk /proc at once.
//
// A lookup that finds a scan already in flight waits for it instead of
// starting a second walk: the first snapshot is the one the second caller
// would have produced, and a burst of flows for one app must not multiply the
// cost by the number of flows. Different UIDs are bounded by processPathScanLimit
// rather than serialised, so unrelated apps still make progress.
//
// The result is published into the cache only when no ResetCache/Close landed
// while the walk was in flight: after a reset the walk's snapshot describes a
// process table the reset was called to forget.
func (s *linuxSearcher) scanProcessPaths(uid uint32) (map[uint32][]string, error) {
	s.scanAccess.Lock()
	if scan, loaded := s.scans[uid]; loaded {
		s.scanAccess.Unlock()
		<-scan.done
		return scan.paths, scan.err
	}
	scan := &processPathsScan{done: make(chan struct{})}
	s.scans[uid] = scan
	s.scanAccess.Unlock()

	s.scanSlots <- struct{}{}
	generation := s.generation.Load()
	processPaths, err := s.scan(uid)
	<-s.scanSlots

	s.cacheAccess.Lock()
	if err == nil && generation == s.generation.Load() {
		s.processPathCache.Add(uid, &uidProcessPaths{entries: processPaths})
	}
	s.cacheAccess.Unlock()

	scan.paths = processPaths
	scan.err = err
	s.scanAccess.Lock()
	delete(s.scans, uid)
	s.scanAccess.Unlock()
	close(scan.done)
	return processPaths, err
}

func (s *linuxSearcher) scan(uid uint32) (map[uint32][]string, error) {
	if s.scanPaths != nil {
		return s.scanPaths(uid)
	}
	return buildProcessPaths(uid)
}

func buildProcessPaths(uid uint32) (map[uint32][]string, error) {
	files, err := os.ReadDir(pathProc)
	if err != nil {
		return nil, err
	}
	buffer := make([]byte, syscall.PathMax)
	processPaths := make(map[uint32][]string)
	for _, file := range files {
		if !file.IsDir() || !isPid(file.Name()) {
			continue
		}
		info, err := file.Info()
		if err != nil {
			if isIgnorableProcError(err) {
				continue
			}
			return nil, err
		}
		if uid != processPathsAllUsers && info.Sys().(*syscall.Stat_t).Uid != uid {
			continue
		}
		processPath := filepath.Join(pathProc, file.Name())
		fdPath := filepath.Join(processPath, "fd")
		exePath, err := os.Readlink(filepath.Join(processPath, "exe"))
		if err != nil {
			if isIgnorableProcError(err) {
				continue
			}
			return nil, err
		}
		fds, err := os.ReadDir(fdPath)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			n, err := syscall.Readlink(filepath.Join(fdPath, fd.Name()), buffer)
			if err != nil {
				continue
			}
			inode, ok := parseSocketInode(buffer[:n])
			if !ok {
				continue
			}
			if !slices.Contains(processPaths[inode], exePath) {
				processPaths[inode] = append(processPaths[inode], exePath)
			}
		}
	}
	return processPaths, nil
}

func isIgnorableProcError(err error) bool {
	return os.IsNotExist(err) || os.IsPermission(err)
}

func parseSocketInode(link []byte) (uint32, bool) {
	const socketPrefix = "socket:["
	if len(link) <= len(socketPrefix) || string(link[:len(socketPrefix)]) != socketPrefix || link[len(link)-1] != ']' {
		return 0, false
	}
	var inode uint64
	for _, char := range link[len(socketPrefix) : len(link)-1] {
		if char < '0' || char > '9' {
			return 0, false
		}
		inode = inode*10 + uint64(char-'0')
		if inode > uint64(^uint32(0)) {
			return 0, false
		}
	}
	return uint32(inode), true
}

func isPid(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool {
		return !unicode.IsDigit(r)
	}) == -1
}
