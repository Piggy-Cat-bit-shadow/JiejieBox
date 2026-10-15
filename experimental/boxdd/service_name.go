package main

// serviceName is the daemon's identity in the platform service manager.
//
// It is a GLOBAL machine resource: on Windows the SCM keys a service by exactly this
// string (HKLM\SYSTEM\CurrentControlSet\Services\<name>), and the event log source, the
// service SID and the protected working directory ACL all derive from it. Two clients
// sharing it cannot be installed side by side - the second `service install` would
// reconfigure the first one's service, and the first one's `service uninstall` would delete
// it.
//
// The fork therefore keeps its own name and the upstream client keeps `sing-box-daemon`.
// Everything user-visible about the service is supplied separately by serviceDisplayName
// and serviceDescriptionText, so this value never has to be spelled for a human.
const serviceName = "jiejiebox-daemon"
