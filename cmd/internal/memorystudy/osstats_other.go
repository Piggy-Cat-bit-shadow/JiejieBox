//go:build !windows && !linux

package main

// On a platform with neither a toolhelp snapshot nor /proc, the honest answer is that the ledger
// does not have the operating system's thread and descriptor counts. Reporting zero would turn
// "the instrument cannot see this here" into "this process owns no threads", and the second one is
// a much better-looking number than the truth.

func platformStaticNotes() map[string]string {
	return map[string]string{
		"process.threads":              "no thread counter is implemented for " + hostPlatform(),
		"process.file_descriptors":     "no descriptor counter is implemented for " + hostPlatform(),
		"process.working_set_bytes":    "no host resident-set counter is implemented for " + hostPlatform(),
		"process.private_commit_bytes": "no host private-memory counter is implemented for " + hostPlatform(),
	}
}

func readHostProcess(readings *processReadings, unsupported map[string]string) {
	readings.Supported = false
	readings.Reason = "operating-system process counters are not implemented for " + hostPlatform()
	unsupported["process.threads"] = readings.Reason
	unsupported["process.file_descriptors"] = readings.Reason
	unsupported["process.working_set_bytes"] = readings.Reason
	unsupported["process.private_commit_bytes"] = readings.Reason
}
