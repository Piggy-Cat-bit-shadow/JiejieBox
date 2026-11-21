package main

// The build-input refusals have to name the input the value came from. Both settings are read from
// a flag OR an environment variable, and an operator who exported the variable must not be sent to
// a flag that is not set: the two failure modes ("I set it and it is still wrong" and "I did not
// set that") look identical when the message names the wrong source.
//
// Found by running the builder, not by reading it:
//
//	JJ_APPLE_STARTUP_POLICY=memprofilerate=abc go run ./cmd/internal/build_libbox -print-startup-policy
//	FATAL resolve -startup-policy: unknown memprofilerate value "abc"
//	JJ_RUNTIME_OVERLAY=experimental go run ./cmd/internal/build_libbox -print-runtime-overlay ios/arm64
//	FATAL resolve -runtime-overlay: unknown runtime overlay mode "experimental"

import (
	"strings"
	"testing"

	"github.com/sagernet/sing-box/cmd/internal/build_shared"
)

func TestStartupPolicyRefusalNamesTheInputItCameFrom(t *testing.T) {
	savedFlag := startupPolicyFlag
	startupPolicyFlag = ""
	defer func() { startupPolicyFlag = savedFlag }()

	t.Setenv(build_shared.StartupPolicyEnvironmentVariable, "memprofilerate=abc")
	_, err := startupPolicyFromEnvironment()
	if err == nil {
		t.Fatal("a bad environment value must fail the build")
	}
	if !strings.Contains(err.Error(), build_shared.StartupPolicyEnvironmentVariable) {
		t.Fatalf("the refusal names the wrong input; it must contain %s: %v", build_shared.StartupPolicyEnvironmentVariable, err)
	}

	startupPolicyFlag = "memprofilerate=abc"
	_, err = startupPolicyFromEnvironment()
	if err == nil {
		t.Fatal("a bad flag value must fail the build")
	}
	if !strings.Contains(err.Error(), "-startup-policy") {
		t.Fatalf("the flag refusal must name the flag: %v", err)
	}
	// The flag must win over a valid environment value, and the environment must win over the
	// reviewed default: the refactor above must not change the precedence.
	startupPolicyFlag = "memprofilerate=off,adaptivestackstart=off"
	t.Setenv(build_shared.StartupPolicyEnvironmentVariable, "memprofilerate=inherit,adaptivestackstart=inherit")
	policy, err := startupPolicyFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if policy.String() != "memprofilerate=0,adaptivestackstart=0" {
		t.Fatalf("the flag was ignored in favour of the environment: %s", policy.String())
	}
}

func TestRuntimeOverlayRefusalNamesTheInputItCameFrom(t *testing.T) {
	savedFlag := runtimeOverlayMode
	runtimeOverlayMode = ""
	defer func() { runtimeOverlayMode = savedFlag }()

	t.Setenv("JJ_RUNTIME_OVERLAY", "experimental")
	_, err := runtimeOverlayModeFromEnvironment()
	if err == nil {
		t.Fatal("a bad environment value must fail the build")
	}
	if !strings.Contains(err.Error(), "JJ_RUNTIME_OVERLAY") {
		t.Fatalf("the refusal names the wrong input; it must contain JJ_RUNTIME_OVERLAY: %v", err)
	}

	runtimeOverlayMode = "experimental"
	_, err = runtimeOverlayModeFromEnvironment()
	if err == nil {
		t.Fatal("a bad flag value must fail the build")
	}
	if !strings.Contains(err.Error(), "-runtime-overlay") {
		t.Fatalf("the flag refusal must name the flag: %v", err)
	}

	runtimeOverlayMode = "small"
	t.Setenv("JJ_RUNTIME_OVERLAY", "base")
	mode, err := runtimeOverlayModeFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if mode != "small" {
		t.Fatalf("the flag did not win over the environment: %s", mode)
	}
}
