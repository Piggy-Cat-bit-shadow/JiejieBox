// Package runtimeoverlay builds the Go source overlay that customises the runtime the
// iOS-family libbox slices are compiled with, and proves the overlay reached the artifact.
//
// # Why this package exists
//
// The iOS packet tunnel lives under a jetsam budget of a few tens of MiB while its Go heap is
// a few MiB. Three of the upstream runtime's choices are sized for a server and cost this
// process more than the heap they serve: the span-set metadata blocks (4 KiB per span set,
// four span sets per mcentral, one mcentral per size class and span class), the per-P stack
// cache block (32 KiB, i.e. two 16 KiB pages on iOS), and keeping an exited goroutine's
// starting stack on the P's free list. A fourth cost is not a size at all: a non-Go thread
// that calls into Go is given an m, and on pthread platforms upstream keeps that m bound to
// the thread until the thread exits, which on iOS means every dispatch thread that ever
// called into the core holds one.
//
// Clash/Hako moved three constants and two code paths in the runtime to address this. This
// package is the same idea re-derived against Go 1.27.4 and fitted to this fork's own build
// chain, because the runtime source is the one dependency that is already installed on the
// build machine and therefore cannot be patched in place.
//
// # The mechanism, and why not something simpler
//
//   - A `replace` directive cannot reach the standard library: `runtime` is not a module.
//   - Copying GOROOT is a second toolchain and drifts from `go.mod`'s pin.
//   - Patching the installed GOROOT is a mutation of shared state that a failed build leaves
//     behind.
//
// `go build -overlay` is the supported way to make the go command read one file with another
// file's contents, and cmd/go also lets an overlay ADD a file to a package directory. gomobile
// invokes `go build`/`go list`/`go run` through PATH, so a shim directory placed first on PATH
// injects `-overlay` into exactly those subcommands (and nothing else). See shim.go.
//
// # What is replaced, and what each replacement is allowed to assume
//
//	mspanset.go    spanSetBlockEntries, spanSetInitSpineCap  -> plan.Params
//	malloc.go      _StackCacheSize                          -> plan.Params
//	proc.go        gfput's stack-free condition             -> plan.Params.FreeExitedStack
//	proc.go        needAndBindM's cgoBindM call             -> plan.Params.DropExtraM
//	proc.go        schedinit calls the marker               -> always when the overlay is on
//	asm_arm64.s    cgocallback consults the flag before dropm -> plan.Params.DropExtraM
//	added files    jjOverlay* identifiers and the marker string -> both build constraints
//
// Every edit is a literal string replacement whose anchor must occur EXACTLY once in the
// toolchain's own file, and the toolchain's file must hash to the reviewed value for its
// version. A different Go minor OR patch version fails closed: the constants below were
// reviewed against one runtime, and "it compiled" is not evidence that the invariant it
// touches is unchanged.
//
// # The two build constraints, and the amd64 exception
//
// The overlay is applied to one gomobile invocation that produces iOS AND macOS slices, so
// the added identifiers exist in two files: the `ios` one carries the plan's values and the
// marker string, and the `!ios` one carries upstream's values and no marker at all. macOS
// therefore keeps the official runtime semantics — there is no per-slice overlay.
//
// Group B is arm64-only by construction. Only asm_arm64.s is edited, so on an arm64
// simulator slice (iossimulator/amd64) the two halves must move together or not at all:
// dropping the m without the assembly half never happens, and suppressing cgoBindM without
// the drop would leak the m instead of releasing it. The added file therefore resolves the
// flag as `<plan wants it> && goarch.IsArm64 == 1`, and the macOS/iOS-amd64 slices take
// upstream's binding path in both halves. This is the one place a build constraint and a
// runtime check are combined deliberately rather than accidentally.
//
// # What proves it worked
//
// A marker string is compiled into every slice the overlay selected, reachable from schedinit
// so no dead-code pass can drop it, and readable with a plain byte search. A symbol-table
// check would not do: release libbox links with `-s -w`, which is exactly the case where `nm`
// stops answering. verify.go checks the marker against the mode recorded in
// libbox.provenance, so "the overlay was requested" and "the bytes contain it" are the same
// claim, and a macOS slice that acquires the marker fails the build as a leaked constraint.
package runtimeoverlay
