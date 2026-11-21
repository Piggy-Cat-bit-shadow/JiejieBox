package main

import (
	"github.com/sagernet/sing-box/cmd/internal/build_shared"
	E "github.com/sagernet/sing/common/exceptions"
)

// startupPolicyFlag is the -startup-policy flag: the runtime startup defaults this Apple build
// links on top of the compatibility defaults. It is a BUILD input (like -runtime-overlay), not a
// product switch: the value is read by runtime.schedinit before any init() runs and cannot be
// changed later, and one libbox framework is shared by the app and the extension, so it is
// process-level rather than per-Box.
var startupPolicyFlag string

// appleStartupPolicy is the policy this run resolved. It is package-level because writeProvenance
// runs after the build and the artifact's record must name what was actually linked.
var appleStartupPolicy = build_shared.DefaultStartupPolicy()

// startupPolicyFromEnvironment applies flag > JJ_APPLE_STARTUP_POLICY > the reviewed default.
//
// The two sourced cases are not parsed by two copies of the precedence: the environment case is
// delegated to build_shared.StartupPolicyFromEnvironment, whose error names
// JJ_APPLE_STARTUP_POLICY. This function used to parse both with the message
// "resolve -startup-policy", so an operator who had exported the variable was sent to a flag that
// was not set - found by running the builder with JJ_APPLE_STARTUP_POLICY=memprofilerate=abc.
func startupPolicyFromEnvironment() (build_shared.StartupPolicy, error) {
	if startupPolicyFlag != "" {
		policy, err := build_shared.ParseStartupPolicy(startupPolicyFlag)
		if err != nil {
			return build_shared.StartupPolicy{}, E.Cause(err, "resolve -startup-policy")
		}
		return policy, nil
	}
	return build_shared.StartupPolicyFromEnvironment()
}

// startupPolicyProvenanceLines are appended to libbox.provenance. The policy is written even when
// it is the inherit default, so an artifact that opted out and an artifact from a build that
// predates this mechanism are distinguishable.
func startupPolicyProvenanceLines() []string {
	if target != "apple" {
		return nil
	}
	return []string{
		"runtime.startup=" + appleStartupPolicy.String(),
		"runtime.godebugDefault=" + appleStartupPolicy.GODEBUGDefaults(),
	}
}
