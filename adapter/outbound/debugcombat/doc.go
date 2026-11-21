// Package debugcombat holds the cross-system attack fixtures that are deliberately outside the
// packages they exercise.
//
// The fixtures reach the production code through its exported surface - the outbound Manager's
// Replace/ReplaceOwned/Remove/UpdateGroupMembership and the runtimecoord Coordinator's
// Register/Advance - and implement the same interfaces the real objects do, so an attack cannot be
// satisfied by a helper the production path does not use.
//
// This file exists so the package is buildable on its own: Go rejects a directory whose only Go
// files are tests, and `go build ./...` would otherwise report "no non-test Go files".
package debugcombat
