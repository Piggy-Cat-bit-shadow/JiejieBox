//nolint:unused
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagernet/sing-box/daemon"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// An access-control decision must not depend on how the peer SPELLED the method.
//
// # The defect, measured
//
// `daemonAuthorizer` chooses the branch that carries the ownership gate by asking whether a
// method belongs to a protected service:
//
//	startedPrefix := "/" + daemon.StartedService_ServiceDesc.ServiceName + "/"
//	return strings.HasPrefix(method, startedPrefix) || ...
//
// The LEADING SLASH is part of that prefix, and the method string is grpc's
// `info.FullMethod`, which is built from the HTTP/2 `:path` in `(*Server).handleStream` AFTER
// a conditional strip:
//
//	sm := stream.Method()
//	if sm != "" && sm[0] == '/' {
//	    sm = sm[1:]                       // <- the slash is removed here
//	}
//	...
//	service := sm[:pos]
//	method := sm[pos+1:]
//
// So `/daemon.StartedService/GetVersion` and `daemon.StartedService/GetVersion` are the SAME
// handler with two different method strings, and only the first satisfied the prefix test.
//
// MEASURED before the fix, with `owner-user` recorded as the owner:
//
//	ownerProtectedMethod("/daemon.StartedService/GetVersion")  = true
//	ownerProtectedMethod("daemon.StartedService/GetVersion")   = false
//	InvokeUnary(owner-user, canonical)  -> nil                                    (admitted)
//	InvokeUnary(owner-user, slashless)  -> PermissionDenied "the service is not available"
//
// # What kind of defect it is: fail-CLOSED, and that is worth stating precisely
//
// The unprotected branch runs `Authorize`, whose fallback denies everything that is neither
// desktop-prefixed nor owner-protected, so a non-owner using the slashless spelling is
// refused as well. There is NO privilege escalation here, and the first version of this file
// asserted the opposite - it read "the handler ran" from a test that was in fact observing
// the deny branch. The correction is recorded rather than quietly dropped, because the
// mistake is the interesting part: a branch that looks unreachable can be reached and still
// deny.
//
// So the harm that IS established is:
//
//   - the OWNER is refused a method grpc routed to the same handler, with a message ("the
//     service is not available") that names a service which is available;
//   - an authorization decision is a function of the caller's spelling, which is one
//     permissive fallback away from the CVE-2026-33186 shape - dispatch and authorization
//     disagreeing about which method is being called. Any future change that widens the
//     fallback (a broader desktop prefix, an allow-list for health or reflection, a "not
//     protected, so allow" default) converts this into that escalation.
//
// The fix makes both sides normalize identically, which is also what makes the deny message
// true.

// ownerTestWorkingDirectory points the owner state at a temp dir and returns it.
func ownerTestWorkingDirectory(t *testing.T, ownerUserID string) string {
	t.Helper()
	directory := t.TempDir()
	content, err := json.Marshal(ownerState{UserID: ownerUserID})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, ownerFileName), content, 0o600); err != nil {
		t.Fatal(err)
	}
	previous := workingDirectory
	workingDirectory = directory
	t.Cleanup(func() { workingDirectory = previous })
	return directory
}

// contextWithPeer builds the context `peerIdentityFromContext` reads.
func contextWithPeer(userID string) context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: &peerAuthInfo{identity: peerIdentity{
			UserID:    userID,
			ProcessID: 4242,
			SessionID: 1,
		}},
	})
}

// TestOwnerProtectedMethodIsSpellingIndependent pins the predicate.
//
// This is the smallest statement of the invariant: whether a method is owner-protected is a
// property of the METHOD, not of the string the peer chose to spell it with.
func TestOwnerProtectedMethodIsSpellingIndependent(t *testing.T) {
	started := daemon.StartedService_ServiceDesc.ServiceName
	managed := daemon.ManagedService_ServiceDesc.ServiceName
	desktop := DesktopService_ServiceDesc.ServiceName

	for _, testCase := range []struct {
		method    string
		protected bool
		why       string
	}{
		{"/" + started + "/GetVersion", true, "the spelling every generated client sends"},
		{started + "/GetVersion", true, "the spelling grpc dispatches to the same handler"},
		{"/" + managed + "/StopService", true, "the other protected service"},
		{managed + "/StopService", true, "and its slashless form"},
		{"/" + desktop + "/GetDaemonInfo", false, "DesktopService is not owner-protected"},
		{desktop + "/GetDaemonInfo", false, "nor in its slashless form"},
		{"/" + started + "X/GetVersion", false, "a service whose name merely starts the same"},
		{"", false, "no method at all"},
	} {
		if got := ownerProtectedMethod(testCase.method); got != testCase.protected {
			t.Errorf("ownerProtectedMethod(%q) = %v, want %v (%s)",
				testCase.method, got, testCase.protected, testCase.why)
		}
	}

	// A double slash is deliberately NOT normalized away: grpc strips exactly one and then
	// splits on the LAST slash, so `//svc/M` looks up the service `/svc`, which nothing
	// registers. Normalizing further here would make this predicate claim a method that
	// dispatch refuses.
	if ownerProtectedMethod("//" + started + "/GetVersion") {
		t.Errorf("a double-slash method must not be treated as protected: grpc cannot dispatch "+
			"it to %s, so the predicate would claim a method that cannot be called",
			daemon.StartedService_ServiceDesc.ServiceName)
	}
}

// TestOwnerGateAdmitsTheOwnerForEitherSpelling is the regression, and it is the half that was
// measurably broken.
//
// The owner must reach an owner-protected method whichever way the path is spelled, because
// grpc routes both to the same handler.
func TestOwnerGateAdmitsTheOwnerForEitherSpelling(t *testing.T) {
	ownerTestWorkingDirectory(t, "owner-user")
	authorizer := &daemonAuthorizer{daemon: &Daemon{}}

	started := daemon.StartedService_ServiceDesc.ServiceName
	managed := daemon.ManagedService_ServiceDesc.ServiceName

	for _, method := range []string{
		"/" + started + "/GetVersion",
		started + "/GetVersion",
		"/" + managed + "/ReloadService",
		managed + "/ReloadService",
	} {
		invoked := false
		_, err := authorizer.InvokeUnary(
			contextWithPeer("owner-user"),
			method,
			func() (any, error) { invoked = true; return "ran", nil },
		)
		if err != nil {
			t.Errorf("%q: the OWNER was refused (%v). Before the fix the slashless spelling took "+
				"the unprotected branch, whose fallback denies everything that is neither "+
				"desktop-prefixed nor owner-protected - a refusal that names a service which is "+
				"in fact available", method, err)
			continue
		}
		if !invoked {
			t.Errorf("%q: no error but the handler did not run", method)
		}
	}
}

// TestOwnerGateRefusesANonOwnerForEitherSpelling is the control on the other side.
//
// It is what makes the test above meaningful: the gate has to be doing work, not merely
// admitting everyone. Both spellings must be refused for a non-owner, and the refusal for an
// owner-protected method must come from the OWNERSHIP check rather than from the "service is
// not available" fallback - otherwise the method was not recognized as protected at all.
func TestOwnerGateRefusesANonOwnerForEitherSpelling(t *testing.T) {
	ownerTestWorkingDirectory(t, "owner-user")
	authorizer := &daemonAuthorizer{daemon: &Daemon{}}

	started := daemon.StartedService_ServiceDesc.ServiceName

	for _, method := range []string{
		"/" + started + "/GetVersion",
		started + "/GetVersion",
	} {
		invoked := false
		_, err := authorizer.InvokeUnary(
			contextWithPeer("attacker-user"),
			method,
			func() (any, error) { invoked = true; return "ran", nil },
		)
		if invoked {
			t.Fatalf("%q: the handler RAN for a non-owner", method)
		}
		if err == nil {
			t.Fatalf("%q: no error and no invocation", method)
		}
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%q: refused with %v, want PermissionDenied", method, err)
		}
		if got := status.Convert(err).Message(); got != "the service is owned by another user" {
			t.Errorf("%q: refused with %q. The ownership gate must be the thing that refused; "+
				"\"the service is not available\" means the method was not recognized as "+
				"owner-protected, which is the defect this file pins", method, got)
		}
	}
}

// TestAuthorizeAgreesForBothSpellings covers the other entry point.
//
// The stream interceptor calls `Authorize` directly, and `InvokeUnary` calls it for anything
// that is not owner-protected, so the same property has to hold there.
func TestAuthorizeAgreesForBothSpellings(t *testing.T) {
	ownerTestWorkingDirectory(t, "owner-user")
	authorizer := &daemonAuthorizer{daemon: &Daemon{}}

	started := daemon.StartedService_ServiceDesc.ServiceName
	desktop := DesktopService_ServiceDesc.ServiceName

	// The property that was violated: the SAME caller must get the SAME answer for the two
	// spellings of one method, because grpc routes both to the same handler. Each pair is
	// checked for an owner and for a non-owner, so the assertion cannot be satisfied by a
	// predicate that simply answers one way for everything.
	for _, pair := range [][3]string{
		{"/" + started + "/GetVersion", started + "/GetVersion", "owner-protected service"},
		{"/" + desktop + "/GetDaemonInfo", desktop + "/GetDaemonInfo", "DesktopService"},
		{"/" + started + "X/GetVersion", started + "X/GetVersion", "an unregistered service"},
	} {
		for _, who := range []string{"owner-user", "attacker-user"} {
			canonicalErr := authorizer.Authorize(contextWithPeer(who), pair[0])
			slashlessErr := authorizer.Authorize(contextWithPeer(who), pair[1])
			if (canonicalErr == nil) != (slashlessErr == nil) {
				t.Errorf("%s (%s): %q gave %v but %q gave %v - the decision depends on the "+
					"spelling of a method grpc dispatches identically",
					pair[2], who, pair[0], canonicalErr, pair[1], slashlessErr)
			}
		}
	}

	// And the decision must still discriminate, or the agreement above would be vacuous.
	if err := authorizer.Authorize(contextWithPeer("owner-user"), "/"+started+"/GetVersion"); err != nil {
		t.Errorf("the owner was refused an owner-protected method: %v", err)
	}
	if err := authorizer.Authorize(contextWithPeer("attacker-user"), "/"+started+"/GetVersion"); err == nil {
		t.Error("a non-owner was admitted to an owner-protected method")
	}
	if err := authorizer.Authorize(contextWithPeer("attacker-user"), "/"+desktop+"/GetDaemonInfo"); err != nil {
		t.Errorf("DesktopService is not owner-protected and must be reachable by any "+
			"authenticated peer, but a non-owner got: %v", err)
	}
}
