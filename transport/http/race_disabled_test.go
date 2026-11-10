//go:build !race

package http

// raceDetectorEnabled is false in a normal test binary; see race_enabled_test.go.
const raceDetectorEnabled = false
