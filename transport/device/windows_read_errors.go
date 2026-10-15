package device

import (
	"errors"
	"syscall"

	"github.com/sagernet/sing-tun"
	E "github.com/sagernet/sing/common/exceptions"
)

// Windows spellings for the TUN read conditions this package classifies.
//
// # Why this exists
//
// `tun.IsRecoverableReadError` compares the POSIX constants:
//
//	sing-tun/tun_read_error.go
//	    return errors.Is(err, ErrTooManySegments) ||
//	           E.IsMulti(err, syscall.EAGAIN, syscall.EINTR, syscall.ENOMEM, syscall.ENOBUFS)
//
// and on Windows none of those ever matches what arrives. MEASURED on this host:
//
//	syscall.EAGAIN   = 0x20000006 (536870918)     a raw 10035 (WSAEWOULDBLOCK) matches: none
//	syscall.EINTR    = 0x20000026 (536870950)     a raw 10004 (WSAEINTR)       matches: none
//	syscall.ENOMEM   = 0x2000004f (536870991)     a raw 10008 (WSAENOMEM)      matches: none
//	syscall.ENOBUFS  = 0x20000046 (536870982)     a raw 10055 (WSAENOBUFS)     matches: none
//
// That is Go's usual Windows errno shape - the POSIX number with the application-defined bit set -
// so a comparison against the POSIX constant is dead code on the platform whose reported failures
// are exactly these. The Windows TUN delivers the raw value:
//
//	sing-tun/tun_windows.go:428    packet, errno := t.session.ReceivePacket()
//	sing-tun/tun_windows.go:447    return 0, fmt.Errorf("read failed: %w", errno)
//
// fmt.Errorf preserves the chain, so `errors.Is(err, syscall.Errno(10055))` DOES match - the value
// is reachable, only the constant being compared is wrong.
//
// # Why this is the same class the fork already paid for
//
// common/dialer/windows_socket_errors.go documents the identical defect for socket errors and fixes
// it with `ErroredWith` and explicit errno sets, reasoning that "a build-tagged version would leave
// the Windows arm untestable from the machine this was fixed on". This file follows that precedent,
// and the constants are written numerically for the same reason: one implementation on every
// platform, testable anywhere.
//
// # What it is NOT allowed to do
//
// A false "recoverable" is worse than a false "fatal" here: this loop would spin forever on a
// genuinely dead device. So the set contains only conditions that mean "this read can be retried",
// and anything ambiguous stays fatal.
//
// The values are from
// https://learn.microsoft.com/en-us/windows/win32/debug/system-error-codes and
// https://learn.microsoft.com/en-us/windows/win32/winsock/windows-sockets-error-codes-2, and are
// written numerically rather than referenced from golang.org/x/sys/windows so that this file
// compiles and is TESTABLE on every platform - the same choice common/dialer/windows_socket_errors.go
// makes and documents.
const (
	// WSAEWOULDBLOCK is WSAEAGAIN: the non-blocking read had no packet. Spinning on this is exactly
	// what the rate limiter and the readWait handle inside the TUN, so reaching the loop with it
	// means "try again", never "the device is gone".
	windowsWSAEWOULDBLOCK = syscall.Errno(10035)
	// ERROR_NOT_ENOUGH_MEMORY is the ERROR_* spelling of ENOMEM. The Windows TUN can surface either
	// namespace depending on which call failed - the Go stack uses the Win32 AFD ioctl (ERROR_*), the
	// native ring uses Winsock (WSA*) - so both are classified.
	windowsERRORNotEnoughMemory = syscall.Errno(8)
	// ERROR_OPERATION_ABORTED is a cancellation, like EINTR: the operation was aborted by a close or a
	// pending cancel. Recording it as fatal would end the datapath on a teardown the caller's closed
	// check may not have observed yet.
	windowsERROROperationAborted = syscall.Errno(995)
)

// readErrorIsRecoverable reports whether a TUN read error is a transient condition the read loop
// should retry, recognising BOTH the POSIX and the Windows spellings of the same facts.
//
// `ErrTooManySegments` and the closed cases are deliberately left to the caller and to
// tun.IsRecoverableReadError, which already handle them correctly on every platform.
func readErrorIsRecoverable(err error) bool {
	if err == nil {
		return false
	}
	if tun.IsRecoverableReadError(err) {
		return true
	}
	return errors.Is(err, windowsWSAEWOULDBLOCK) ||
		errors.Is(err, windowsERRORNotEnoughMemory) ||
		errors.Is(err, windowsERROROperationAborted) ||
		E.IsMulti(err, syscall.EAGAIN, syscall.EINTR, syscall.ENOMEM, syscall.ENOBUFS)
}

// The two Windows constants this file does NOT treat as recoverable, recorded so the omission is a
// decision rather than an oversight:
//
//	ERROR_BUFFER_OVERFLOW (111)   the Go stack RETRIES this internally
//	                              (sing-tun/stack_go_io_windows.go:370 returns to the caller only
//	                              when err is nil, ERROR_BUFFER_OVERFLOW or os.ErrClosed), so
//	                              reaching a read loop with it means the stack gave up - a state
//	                              where spinning is the wrong answer.
//	ERROR_HANDLE_EOF              sing-tun already converts it to os.ErrClosed, which the caller
//	                              classifies as fatal before asking here.
