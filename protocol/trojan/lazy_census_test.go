package trojan

// Coverage census for the deferred path: how many nodes of a real subscription the deferred
// constructor would actually own, and why the rest fall back.
//
// # Why this is a pure query
//
// The question "how much of this subscription is deferred" must be answerable from a MANAGEMENT view
// without building a single node: LazyEligible validates and copies a recipe, and nothing else here
// constructs a TLS config, dials, or resolves. A census that expanded or dialled would answer a
// different question - and it would be the same defect as a metadata query that builds.
//
// # Where the corpus comes from
//
// A real subscription is user data and is not in this repository, so the corpus is an INPUT:
//
//	$env:HAKO_GM4_CORPUS='C:\path\to\subscription.json'   # one document
//	$env:HAKO_GM4_CORPUS='C:\path\to\dir'                 # every *.json in it
//	$env:HAKO_GM4_EVIDENCE_DIR='...'                      # optional CSV destination
//	go test -tags <study tags> -run TestCensusLazyEligibility -v ./protocol/trojan
//
// Without the input the test prints the BLOCKED reason and skips. It never invents a coverage number:
// a synthetic shape matrix is reported separately and labelled as such.

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/provider/parser"

	"github.com/stretchr/testify/require"
)

// censusReport is the per-corpus tally. Total counts every outbound in the document, so the row also
// says how much of the subscription is TROJAN - a coverage number over trojan nodes alone would hide
// that the deferred path is one protocol.
type censusReport struct {
	documents   int
	outbounds   int
	trojan      int
	eligible    int
	byReason    map[string]int
	byTypeCount map[string]int
}

func newCensusReport() *censusReport {
	return &censusReport{byReason: make(map[string]int), byTypeCount: make(map[string]int)}
}

func (r *censusReport) census(ctx context.Context, logger interface{}, outbounds []option.Outbound) {
	for _, entry := range outbounds {
		r.outbounds++
		r.byTypeCount[entry.Type]++
		if entry.Type != C.TypeTrojan {
			continue
		}
		r.trojan++
		trojanOptions, isTrojan := entry.Options.(*option.TrojanOutboundOptions)
		if !isTrojan {
			r.byReason["not-trojan-options"]++
			continue
		}
		eligible, reason := LazyEligible(ctx, trojanOptions)
		if eligible {
			r.eligible++
			continue
		}
		if reason == "" {
			reason = "unknown"
		}
		r.byReason[reason]++
	}
}

func (r *censusReport) logLines() []string {
	lines := []string{
		fmt.Sprintf("CENSUS documents=%d outbounds=%d trojan=%d eligible=%d coverage=%.3f%%",
			r.documents, r.outbounds, r.trojan, r.eligible, 100*float64(r.eligible)/float64(max(r.trojan, 1))),
	}
	types := make([]string, 0, len(r.byTypeCount))
	for outboundType := range r.byTypeCount {
		types = append(types, outboundType)
	}
	sort.Strings(types)
	for _, outboundType := range types {
		lines = append(lines, fmt.Sprintf("CENSUS type=%s count=%d", outboundType, r.byTypeCount[outboundType]))
	}
	reasons := make([]string, 0, len(r.byReason))
	for reason := range r.byReason {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		lines = append(lines, fmt.Sprintf("CENSUS fallback-reason=%s count=%d", reason, r.byReason[reason]))
	}
	return lines
}

// TestCensusLazyEligibility answers "how much of this subscription can be deferred" from a real
// document, without constructing anything. It is skipped - with the reason printed - when no corpus is
// supplied, because a coverage claim needs a corpus and there is none in the repository.
func TestCensusLazyEligibility(t *testing.T) {
	corpus := os.Getenv("HAKO_GM4_CORPUS")
	if corpus == "" {
		t.Log("CENSUS BLOCKED_BY_ENVIRONMENT: no subscription corpus. Set HAKO_GM4_CORPUS to a sing-box " +
			"subscription document (or a directory of *.json) to measure real coverage. This test does not " +
			"substitute a synthetic corpus for it; the shape matrix below is reported separately and is not " +
			"a coverage number.")
		censusSyntheticShapes(t)
		t.Skip("no corpus")
	}
	ctx := context.Background()
	report := newCensusReport()
	documents := []string{}
	info, err := os.Stat(corpus)
	require.NoError(t, err)
	if info.IsDir() {
		entries, readErr := os.ReadDir(corpus)
		require.NoError(t, readErr)
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
				continue
			}
			documents = append(documents, filepath.Join(corpus, entry.Name()))
		}
	} else {
		documents = append(documents, corpus)
	}
	require.NotEmpty(t, documents, "the corpus directory contains no *.json document")
	for _, document := range documents {
		content, readErr := os.ReadFile(document)
		require.NoError(t, readErr)
		outbounds, _, parseErr := parser.ParseBoxSubscription(ctx, string(content))
		if parseErr != nil {
			// A link-based or base64 document is not a sing-box document; parsing it as one is not a
			// failure of this census, but the document must not be counted as "zero eligible".
			t.Logf("CENSUS document=%s parse=skipped reason=%v", filepath.Base(document), parseErr)
			continue
		}
		report.documents++
		report.census(ctx, log.NewNOPFactory().NewLogger("census"), outbounds)
	}
	for _, line := range report.logLines() {
		t.Log(line)
	}
	writeCensusCSV(t, report)
}

// censusSyntheticShapes runs the same declaration over the shape corpus the eligibility tests use, so
// the fallback reasons are executable evidence. It is labelled synthetic everywhere it is reported.
func censusSyntheticShapes(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	var outbounds []option.Outbound
	for name, options := range lazyShapeCorpus() {
		outbounds = append(outbounds, option.Outbound{Type: C.TypeTrojan, Tag: name, Options: options})
	}
	report := newCensusReport()
	report.documents = 1
	report.census(ctx, log.NewNOPFactory().NewLogger("census"), outbounds)
	for _, line := range report.logLines() {
		t.Log("CENSUS(synthetic) " + strings.TrimPrefix(line, "CENSUS "))
	}
}

func writeCensusCSV(t *testing.T, report *censusReport) {
	t.Helper()
	dir := os.Getenv("HAKO_GM4_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	require.NoError(t, os.MkdirAll(dir, 0o755))
	file, err := os.Create(filepath.Join(dir, "gm4-census.csv"))
	require.NoError(t, err)
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()
	_ = writer.Write([]string{"documents", "outbounds", "trojan", "eligible", "fallback_reason", "count"})
	_ = writer.Write([]string{fmt.Sprint(report.documents), fmt.Sprint(report.outbounds), fmt.Sprint(report.trojan), fmt.Sprint(report.eligible), "", ""})
	reasons := make([]string, 0, len(report.byReason))
	for reason := range report.byReason {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		_ = writer.Write([]string{"", "", "", "", reason, fmt.Sprint(report.byReason[reason])})
	}
}

// lazyShapeCorpus is the option-shape matrix the eligibility tests and the synthetic census share:
// every shape that must fall back, plus the representative eligible one. It lives here so a new
// fallback reason cannot be added to the constructor without appearing in the census.
func lazyShapeCorpus() map[string]*option.TrojanOutboundOptions {
	// Every shape gets its OWN TLS object. A shared pointer here would make one shape's mutation
	// (setting UTLS, say) change every other shape too, and the census would then report a single
	// reason for the whole corpus - which is what this test exists to catch in the constructor, so it
	// must not be what the corpus itself does.
	newShape := func() *option.TrojanOutboundOptions {
		return &option.TrojanOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
			Password:      "password",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{Enabled: true, Insecure: true, ServerName: "cold.example"},
			},
		}
	}
	corpus := map[string]*option.TrojanOutboundOptions{"eligible-plain": newShape()}

	withTransport := newShape()
	withTransport.Transport = &option.V2RayTransportOptions{Type: "ws"}
	corpus["transport"] = withTransport

	withMultiplex := newShape()
	withMultiplex.Multiplex = &option.OutboundMultiplexOptions{Enabled: true}
	corpus["multiplex"] = withMultiplex

	noTLS := newShape()
	noTLS.TLS = nil
	corpus["tls-disabled"] = noTLS

	utls := newShape()
	utls.TLS.UTLS = &option.OutboundUTLSOptions{Enabled: true}
	corpus["tls-exotic"] = utls

	certificate := newShape()
	certificate.TLS.CertificatePath = "/etc/cert.pem"
	corpus["tls-certificate"] = certificate

	versions := newShape()
	versions.TLS.MinVersion = "1.3"
	corpus["tls-version-or-ciphers"] = versions

	fragment := newShape()
	fragment.TLS.Fragment = true
	corpus["tls-fragment-or-spoof"] = fragment

	bindInterface := newShape()
	bindInterface.BindInterface = "utun9"
	corpus["dialer-options"] = bindInterface

	resolver := newShape()
	resolver.DomainResolver = &option.DomainResolveOptions{Server: "dns-out"}
	corpus["dialer-options-resolver"] = resolver

	emptyServer := newShape()
	emptyServer.Server = ""
	corpus["server-empty"] = emptyServer

	return corpus
}

// TestShapeCorpusIsCensused keeps the corpus and the constructor in step: every shape in the corpus
// must produce a census row (eligible, or a named fallback reason). A shape added to the constructor
// without a reason, or a corpus entry that silently becomes eligible, fails here.
func TestShapeCorpusIsCensused(t *testing.T) {
	ctx := context.Background()
	corpus := lazyShapeCorpus()
	eligible := 0
	for name, options := range corpus {
		isEligible, reason := LazyEligible(ctx, options)
		if isEligible {
			eligible++
			require.Equal(t, "eligible-plain", name, "only the representative shape may be eligible in this corpus")
			continue
		}
		require.NotEmpty(t, reason, "shape %s fell back without a reason", name)
	}
	require.Equal(t, 1, eligible)
	// The reason strings are what a management view shows, so they are part of the API surface.
	_, reason := LazyEligible(ctx, corpus["transport"])
	require.Equal(t, "transport", reason)
	_, reason = LazyEligible(ctx, corpus["multiplex"])
	require.Equal(t, "multiplex", reason)
	_, reason = LazyEligible(ctx, corpus["dialer-options"])
	require.Equal(t, "dialer-options", reason)
}
