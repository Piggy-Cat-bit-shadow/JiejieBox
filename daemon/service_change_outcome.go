package daemon

// The apply outcome, as an embedder reads it.
//
// `ApplyServiceChange` returns the report this call produced, but a gomobile bridge cannot return a
// document AND an error: the generated Swift method throws on a non-nil error and the result is
// lost. An embedder would then be back to reading `GetLastServiceChange` after the fact, which is
// exactly the weak receipt this entry exists to replace.
//
// So the bridge carries the outcome in ONE value, with the error inside it:
//
//	{"version":1,"ok":false,"error":"...","report":{...}}
//
// `ok` answers the question a caller actually asks - "is the document I asked for the one running
// now" - and it is deliberately NOT the same thing as "the call returned no error". A recovered
// start returns no error and a `recovered` report: the service is running, but it is running the
// stored last-good document, not the requested one. `interrupted` likewise returns no error and
// leaves nothing running. Both are `ok:false`; the `status` in the report is what tells them apart,
// and an embedder that collapsed them into "failed" would describe a working service as dead.

import (
	"encoding/json"
)

// ServiceChangeOutcomeVersion is the version of the outcome document below.
//
// It is versioned because the fields will grow: a client that decodes it must be able to tell a
// document it understands from a newer one, and `report` itself is the kernel's own JSON whose
// fields are additive. Bumping this version is how an incompatible change to the ENVELOPE is
// announced; adding a field to `report` is not.
const ServiceChangeOutcomeVersion = 1

// ServiceChangeOutcome is one apply attempt as an embedder reads it.
type ServiceChangeOutcome struct {
	Version int    `json:"version"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	// Status repeats the report's status at the top level, so a caller that only needs to
	// distinguish "recovered" from "nothing is running" does not have to decode a nested document
	// whose shape depends on the kernel version.
	Status string               `json:"status,omitempty"`
	Report *ServiceChangeReport `json:"report,omitempty"`
}

// DescribeServiceChangeOutcome builds the outcome for one apply attempt.
//
// `report` may be nil when the attempt never reached the point of describing itself (the service
// was already closed), and `err` may be nil for an attempt that did not apply the requested
// document - both are ordinary, and neither is turned into the other.
func DescribeServiceChangeOutcome(report *ServiceChangeReport, err error) ServiceChangeOutcome {
	outcome := ServiceChangeOutcome{Version: ServiceChangeOutcomeVersion, Report: report}
	if err != nil {
		outcome.Error = err.Error()
	}
	if report != nil {
		outcome.Status = string(report.Status)
	}
	outcome.OK = err == nil && reportSaysRequestedDocumentRuns(report)
	return outcome
}

// reportSaysRequestedDocumentRuns reports whether the requested document is the running one.
//
// `unchanged` counts: the running document already IS the requested one, which is the outcome a
// caller wants when it re-applies what is already in force.
func reportSaysRequestedDocumentRuns(report *ServiceChangeReport) bool {
	if report == nil {
		return false
	}
	switch report.Status {
	case ServiceChangeStatusStarted, ServiceChangeStatusRestarted,
		ServiceChangeStatusAppliedLive, ServiceChangeStatusUnchanged:
		return true
	case ServiceChangeStatusRecovered, ServiceChangeStatusInterrupted, ServiceChangeStatusFailed:
		return false
	default:
		// A status this version does not know is not evidence that the requested document runs.
		return false
	}
}

// EncodeServiceChangeOutcome renders the outcome as the document a bridge returns.
//
// The fallback exists because the alternative to a marshal failure here is either a nil document
// (which a caller reads as "no receipt") or a panic in the tunnel process. Neither is acceptable,
// and the failure is describable in the same shape as any other refusal.
func EncodeServiceChangeOutcome(outcome ServiceChangeOutcome) string {
	encoded, err := json.Marshal(outcome)
	if err != nil {
		fallback, _ := json.Marshal(ServiceChangeOutcome{
			Version: ServiceChangeOutcomeVersion,
			Error:   "the apply outcome could not be encoded: " + err.Error(),
		})
		return string(fallback)
	}
	return string(encoded)
}
