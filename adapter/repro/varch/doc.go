// Package varch holds the independent reviewer's external tests for the node-architecture round.
//
// It lives under the `adapter` source root rather than at the repository top level for two reasons
// the CI gates enforce: a new top-level package root is a module-layout change
// (`cmd/internal/modlayout`), and a package with tests that no workflow runs is a gate failure
// (`scripts/ci/check-package-execution.py`). Under `adapter/...` both hold, and the tests stay
// external: every file is `package varch_test` and reaches the implementation through exported APIs
// only, which is what makes the review independent of the code it reviews.
//
// The tests are kept in the repository so the next round can re-run the attacks instead of trusting
// a report: the canonical encoder must agree with `reflect.DeepEqual` inside its declared domain and
// must never panic outside it; store refs and leases must not cross a generation; and the
// single-Scope lifecycle must keep every close and cancellation rule of the two-Scope baseline.
package varch
