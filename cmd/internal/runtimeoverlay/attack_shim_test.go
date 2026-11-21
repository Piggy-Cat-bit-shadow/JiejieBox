package runtimeoverlay

// End-to-end attacks on the `go` shim: the shim is the only thing that makes the overlay reach the
// compiler, and it is a POSIX shell script built from paths that may contain spaces, non-ASCII
// letters and apostrophes.
//
// The shipping test (prepare_test.go) asserts the shim's SOURCE SHAPE. This file runs it: a real
// module whose main.go is replaced BY the overlay proves the injected -overlay reached the
// compiler, the subcommand allow-list is closed, arguments survive, and the real go's exit status
// is propagated. That last one matters because a shim that always exits 0 would turn "the build
// failed" into "the build produced a stale artifact" for whatever ran next.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// posixShell finds a shell that can run the shim. On a POSIX host that is /bin/sh; on Windows the
// shim still has to be tested (it is written for the Apple build host, but its quoting is the
// thing under test) and Git for Windows ships one.
func posixShell(t *testing.T) string {
	t.Helper()
	if shell, err := exec.LookPath("sh"); err == nil {
		return shell
	}
	if runtime.GOOS == "windows" {
		for _, candidate := range []string{
			`C:\Program Files\Git\usr\bin\sh.exe`,
			`C:\Program Files\Git\bin\bash.exe`,
			`C:\Program Files (x86)\Git\usr\bin\sh.exe`,
		} {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	t.Skip("no POSIX shell is available; the go shim is a /bin/sh script and cannot be exercised here")
	return ""
}

// copyGoBinary copies the real go command into a directory whose name contains spaces, an
// apostrophe and a non-ASCII letter, which is the path the shim has to quote. It returns the
// original path and the copy.
func copyGoBinary(t *testing.T, dir string) (string, string) {
	t.Helper()
	realGo := goBinary(t)
	name := "go"
	if runtime.GOOS == "windows" {
		name = "go.exe"
	}
	target := filepath.Join(dir, "real gö 空's "+name)
	data, err := os.ReadFile(realGo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return realGo, target
}

func TestGoShimInjectsOnlyTheCompilingSubcommands(t *testing.T) {
	if testing.Short() {
		t.Skip("this test builds a module through the shim; -short skips it")
	}
	shell := posixShell(t)
	realGo := goBinary(t)
	toolchain := hostToolchain(t)
	goVersion := strings.TrimSpace(readGoEnv(t, realGo, "GOVERSION"))
	// This toolchain's go binary is trimmed, so a COPY of it cannot find GOROOT by itself. That is
	// a property of the toolchain, not of the shim, so it is stated explicitly in the environment
	// instead of being mistaken for a shim failure.
	baseEnv := append(os.Environ(), "GOROOT="+toolchain.GOROOT, "GOTOOLCHAIN=local", "GOFLAGS=", "GO111MODULE=on")

	// A module in a path with spaces, an apostrophe and non-ASCII letters. go.mod has no
	// dependencies, so the only thing that can make the build fail is the shim.
	moduleDir := filepath.Join(t.TempDir(), "mod ule 空 格's")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {\n\tfmt.Println(\"ORIGINAL\")\n\tfor _, argument := range os.Args[1:] {\n\t\tfmt.Println(\"arg=\" + argument)\n\t}\n}\n"
	overlaid := strings.Replace(original, "ORIGINAL", "OVERLAID", 1)
	overlayDir := filepath.Join(t.TempDir(), "overlay 空's dir")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(moduleDir, "main.go")
	// The replacement lives OUTSIDE the module: a second .go file beside main.go would be compiled
	// as another declaration of main and the build would fail for a reason that is not the shim.
	replacementPath := filepath.Join(overlayDir, "overlaid_main.go")
	if err := os.WriteFile(mainPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacementPath, []byte(overlaid), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte("module shimprobe\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overlayJSON := filepath.Join(overlayDir, "overlay.json")
	payload, err := json.Marshal(map[string]map[string]string{
		"Replace": {mainPath: replacementPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overlayJSON, append(payload, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	shimRoot := filepath.Join(t.TempDir(), "shim 空's root")
	if err := os.MkdirAll(shimRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	_, realGoCopy := copyGoBinary(t, shimRoot)
	// The copy must be a working go command, or every assertion below would be about a broken
	// binary rather than about the shim.
	checkCopy := exec.Command(realGoCopy, "env", "GOROOT")
	checkCopy.Env = baseEnv
	if output, err := checkCopy.CombinedOutput(); err != nil {
		t.Skipf("a copied go command does not run on this host (%v: %s); the shim cannot be exercised with a quoted path", err, output)
	}
	injectedLog := filepath.Join(shimRoot, "injections.log")
	binDir, err := WriteGoShim(shimRoot, realGoCopy, overlayJSON, injectedLog)
	if err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(binDir, "go")
	run := func(args ...string) (string, int) {
		command := exec.Command(shell, append([]string{shim}, args...)...)
		command.Dir = moduleDir
		command.Env = baseEnv
		output, runErr := command.CombinedOutput()
		code := 0
		if runErr != nil {
			exit, ok := runErr.(*exec.ExitError)
			if !ok {
				t.Fatalf("running the shim: %v", runErr)
			}
			code = exit.ExitCode()
		}
		return string(output), code
	}
	injections := func() int {
		data, err := os.ReadFile(injectedLog)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return len(strings.Fields(string(data)))
	}
	requireNoInjection := func(name string) {
		t.Helper()
		if got := injections(); got != 0 {
			t.Fatalf("%s was injected into although it is not a compiling subcommand: log has %d entries", name, got)
		}
	}

	// 1. `go env` is what gomobile uses to DISCOVER the toolchain. Injecting an overlay here would
	// corrupt the discovery the overlay depends on.
	output, code := run("env", "GOROOT")
	if code != 0 {
		t.Fatalf("go env through the shim failed with %d:\n%s", code, output)
	}
	if strings.TrimSpace(output) != strings.TrimSpace(readGoEnv(t, realGo, "GOROOT")) {
		t.Fatalf("go env through the shim disagreed with the real go:\n%s", output)
	}
	requireNoInjection("go env")

	// 2. `go version` is also discovery.
	output, code = run("version")
	if code != 0 {
		t.Fatalf("go version through the shim failed with %d:\n%s", code, output)
	}
	if !strings.Contains(output, goVersion) {
		t.Fatalf("go version through the shim produced %q, want it to name %s", output, goVersion)
	}
	requireNoInjection("go version")

	// 3. `go build` must compile the OVERLAID main.go, which is the whole injection claim.
	binary := filepath.Join(moduleDir, "probe 空.exe")
	output, code = run("build", "-o", binary, ".")
	if code != 0 {
		t.Fatalf("go build through the shim failed with %d:\n%s", code, output)
	}
	if got := injections(); got != 1 {
		t.Fatalf("go build was injected into %d times, want 1", got)
	}
	built, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("the built binary did not run: %v\n%s", err, built)
	}
	if !strings.Contains(string(built), "OVERLAID") {
		t.Fatalf("the overlay did not reach the compiler: the binary printed\n%s", built)
	}

	// 4. `go run` is injected too, and its arguments survive the shim word for word. A quoting bug
	// in shellQuote/WithShimPath would show up here as a mangled or split argument.
	//
	// One Windows-only limitation is stated rather than hidden: MSYS's sh (Git for Windows) parses
	// its own argv out of a Windows command line and treats a bare `'` as the start of a quote, so
	// an argument containing an apostrophe is already merged before this script runs. That is the
	// harness's parser, not the shim: the product shim runs on macOS, where execve passes argv
	// directly. The apostrophe that matters - the one in the `real=` path - IS exercised here,
	// because the copied go command cannot run at all unless that assignment quotes correctly.
	arguments := []string{"arg with space", "üñïcode", "$HOME", "semi;colon", "star*glob"}
	if runtime.GOOS != "windows" {
		arguments = append(arguments, "quote'arg")
	}
	before := injections()
	output, code = run(append([]string{"run", "."}, arguments...)...)
	if code != 0 {
		t.Fatalf("go run through the shim failed with %d:\n%s", code, output)
	}
	if got := injections() - before; got != 1 {
		t.Fatalf("go run was injected into %d times, want 1", got)
	}
	if !strings.Contains(output, "OVERLAID") {
		t.Fatalf("go run did not compile the overlaid source:\n%s", output)
	}
	for _, argument := range arguments {
		if !strings.Contains(output, "arg="+argument) {
			t.Fatalf("go run lost %q through the shim:\n%s", argument, output)
		}
	}

	// 5. `go list` is in the closed set (it is a compiling/scanning subcommand gomobile uses).
	before = injections()
	output, code = run("list", "./...")
	if code != 0 {
		t.Fatalf("go list through the shim failed with %d:\n%s", code, output)
	}
	if !strings.Contains(output, "shimprobe") {
		t.Fatalf("go list through the shim produced %q", output)
	}
	if got := injections() - before; got != 1 {
		t.Fatalf("go list was injected into %d times, want 1", got)
	}

	// 6. A failing go invocation must keep its non-zero exit status. A shim that swallowed it
	// would make a failed Apple build look successful until the missing artifact was noticed.
	before = injections()
	output, code = run("build", "./does-not-exist")
	if code == 0 {
		t.Fatalf("a failing go build exited 0 through the shim:\n%s", output)
	}
	if got := injections() - before; got != 1 {
		t.Fatalf("the failing go build was injected into %d times, want 1", got)
	}
	direct := exec.Command(realGo, "build", "./does-not-exist")
	direct.Dir = moduleDir
	direct.Env = baseEnv
	_, directErr := direct.CombinedOutput()
	exit, ok := directErr.(*exec.ExitError)
	if !ok {
		t.Fatalf("the control invocation did not fail: %v", directErr)
	}
	if code != exit.ExitCode() {
		t.Fatalf("the shim changed the exit status: %d through the shim, %d directly", code, exit.ExitCode())
	}

	// 7. An invocation with no subcommand must be passed through untouched, not turned into
	// `go -overlay=...` with an empty subcommand.
	before = injections()
	output, code = run()
	if code == 0 {
		t.Fatalf("`go` with no arguments should not succeed through the shim:\n%s", output)
	}
	if got := injections() - before; got != 0 {
		t.Fatalf("an empty invocation was logged as an injection: %d new entries", got)
	}
}

func readGoEnv(t *testing.T, goBinary, name string) string {
	t.Helper()
	output, err := exec.Command(goBinary, "env", name).Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func TestWriteGoShimRefusesAnUnusableDestination(t *testing.T) {
	if _, err := WriteGoShim(t.TempDir(), "", "overlay.json", "log"); err == nil {
		t.Fatal("a shim with no real go command must be refused")
	}
	if _, err := WriteGoShim(t.TempDir(), "go", "", "log"); err == nil {
		t.Fatal("a shim with no overlay path must be refused")
	}
}
