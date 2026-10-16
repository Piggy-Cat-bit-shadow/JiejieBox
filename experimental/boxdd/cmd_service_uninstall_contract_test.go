package main

// The contract this test pins, and why it is a source-level test.
//
// serviceUninstall() used to stop the service like this:
//
//	err = stopServiceAndWait(service)
//	if err != nil {
//	    log.Warn("stop service: ", err)      // <- swallowed
//	}
//	err = service.Delete()
//	...
//	return nil                               // <- reports success
//
// A stop that failed or timed out therefore produced `service uninstall` exit 0 while the daemon
// was still RUNNING, its PID still alive and its SCM entry still present. The Windows installer
// reads that exit code as "the service is gone", so the installer lifecycle gate observed an
// uninstall that reported success and left a running service behind. Because the error was
// replaced by a warning, the state, process ID and exit codes that waitServiceState had already
// collected were discarded, so the record could not say why the daemon would not stop.
//
// THE TEST IS SOURCE-LEVEL ON PURPOSE. Exercising the real path needs a Windows SCM, a real
// service and a daemon that refuses to stop, none of which a unit test can arrange; and a
// `//go:build windows` test would run only on the Windows CI legs, while the regression is a
// property of the code rather than of the platform. Reading the file keeps this portable and
// fast, and it fails for the one reason it exists.
//
// It asserts the SHAPE at the level that was wrong - is the stop error returned, and is it
// returned before the delete - and deliberately does not assert how the error is worded.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	serviceUninstallSourcePath = "cmd_service_windows.go"
	serviceUninstallSignature  = "func serviceUninstall() error {"

	// The wrap label serviceUninstall must use. A literal rather than a shared symbol: a shared
	// one would have to live in the windows-tagged file, and this test carries no build tag.
	stopServiceFailedLabel = "stop service"

	// The exact expression the stop failure must produce, built from the label so the two cannot
	// drift apart within this file.
	stopFailureReturn = `return E.Cause(err, "` + stopServiceFailedLabel + `")`
)

// stripLineComments removes whole-line `//` comments.
//
// Necessary because the implementation's comment deliberately quotes the old
// `log.Warn("stop service: ", err)` line to explain why it was removed - and an assertion that
// searched the raw text would find that quotation and report the bug as present. Only whole-line
// comments are stripped, which is all that is needed here: the assertions are about statements,
// and every statement in this function stands on its own line.
func stripLineComments(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func readServiceUninstallBody(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(serviceUninstallSourcePath)
	require.NoError(t, err, "cannot read %s; run this test from the package directory", serviceUninstallSourcePath)

	source := string(content)
	start := strings.Index(source, serviceUninstallSignature)
	require.GreaterOrEqual(t, start, 0,
		"%s not found in %s. The test needs updating; the contract has not changed.",
		serviceUninstallSignature, serviceUninstallSourcePath)

	// From the signature to the next top-level func, so the assertions below are about this
	// function and cannot be satisfied by something elsewhere in the file.
	rest := source[start+len(serviceUninstallSignature):]
	if end := strings.Index(rest, "\nfunc "); end >= 0 {
		rest = rest[:end]
	}
	return stripLineComments(rest)
}

func TestServiceUninstallFailsWhenTheServiceWillNotStop(t *testing.T) {
	body := readServiceUninstallBody(t)

	require.Contains(t, body, stopFailureReturn,
		"serviceUninstall must RETURN the error from stopServiceAndWait. If it only logs the "+
			"error and continues, `service uninstall` exits 0 with the daemon still running - the "+
			"exact failure the Windows installer lifecycle gate kept reporting.")

	require.NotContains(t, body, `log.Warn("stop service: "`,
		"the swallowed-stop warning is back. It must not exist alongside the return: a "+
			"warn-and-continue path lets a failed stop be followed by a delete and a nil return.")
}

func TestServiceUninstallStopsBeforeItDeletes(t *testing.T) {
	body := readServiceUninstallBody(t)

	stopAt := strings.Index(body, "stopServiceAndWait(service)")
	deleteAt := strings.Index(body, "service.Delete()")
	require.GreaterOrEqual(t, stopAt, 0, "serviceUninstall no longer calls stopServiceAndWait")
	require.GreaterOrEqual(t, deleteAt, 0, "serviceUninstall no longer calls service.Delete()")
	require.Less(t, stopAt, deleteAt,
		"the stop must be attempted before the delete. Deleting a service that is still running "+
			"only marks it for deletion: the process keeps running and the SCM entry persists "+
			"until every handle closes.")

	// The failure must be unreachable past the stop: the return has to sit between the two calls,
	// so a service that would not stop can never be deleted and reported as a success.
	require.Contains(t, body[stopAt:deleteAt], stopFailureReturn,
		"the stop failure is not returned before service.Delete(), so a service that would not "+
			"stop can still be deleted and the uninstall can still report success.")
}
