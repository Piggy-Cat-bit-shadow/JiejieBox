package daemon

// C04 — the restart-required domains: DNS policy and routing rules.

import (
	"testing"

	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

func dnsDocument(servers ...option.DNSServerOptions) *option.Options {
	return &option.Options{
		DNS: &option.DNSOptions{RawDNSOptions: option.RawDNSOptions{Servers: servers}},
	}
}

func TestDNSDomainIsUnchangedWhenNothingChanged(t *testing.T) {
	current := dnsDocument(option.DNSServerOptions{Tag: "local", Type: "local"})
	target := dnsDocument(option.DNSServerOptions{Tag: "local", Type: "local"})
	plan := PlanDNSDomain(current, "rev-a", target, "rev-b")
	require.True(t, plan.Supported)
	require.False(t, plan.RestartRequired)
	require.Empty(t, plan.Changes)

	report, err := (&StartedService{}).ApplyDNSDomain(plan, "rev-a")
	require.NoError(t, err)
	require.Equal(t, DomainStatusUnchanged, report.Status)
}

func TestDNSDomainRequiresARestartWhenAServerChanges(t *testing.T) {
	current := dnsDocument(option.DNSServerOptions{Tag: "local", Type: "local"})
	target := dnsDocument(
		option.DNSServerOptions{Tag: "local", Type: "local"},
		option.DNSServerOptions{Tag: "remote", Type: "udp"},
	)
	plan := PlanDNSDomain(current, "rev-a", target, "rev-b")
	require.True(t, plan.RestartRequired, "DNS is owned by the Box, so a new server needs a reload")
	require.NotEmpty(t, plan.Changes)

	report, err := (&StartedService{}).ApplyDNSDomain(plan, "rev-a")
	require.NoError(t, err)
	require.Equal(t, DomainStatusRestartRequired, report.Status)
	require.True(t, report.RestartRequired)
	require.Empty(t, report.Applied, "a domain without a live owner never claims it applied anything")
}

func TestRuleDomainRequiresARestartWhenTheRuleSetListChanges(t *testing.T) {
	current := &option.Options{Route: &option.RouteOptions{Final: "direct"}}
	target := &option.Options{Route: &option.RouteOptions{
		Final: "direct",
		RuleSet: []option.RuleSet{
			{Type: "remote", Tag: []string{"geosite"}, Format: "binary"},
		},
	}}
	plan := PlanRuleDomain(current, "rev-a", target, "rev-b")
	require.True(t, plan.RestartRequired)
	require.NotEmpty(t, plan.Changes)

	report, err := (&StartedService{}).ApplyRuleDomain(plan, "rev-a")
	require.NoError(t, err)
	require.Equal(t, DomainStatusRestartRequired, report.Status)
}

func TestRuleDomainIsUnchangedWithoutEdits(t *testing.T) {
	document := &option.Options{Route: &option.RouteOptions{Final: "direct"}}
	plan := PlanRuleDomain(document, "", document, "")
	require.False(t, plan.RestartRequired)
	report, err := (&StartedService{}).ApplyRuleDomain(plan, "")
	require.NoError(t, err)
	require.Equal(t, DomainStatusUnchanged, report.Status)
}

func TestAStaleDNSPlanIsRefused(t *testing.T) {
	plan := PlanDNSDomain(dnsDocument(), "rev-a", dnsDocument(option.DNSServerOptions{Tag: "x", Type: "local"}), "rev-b")
	report, err := (&StartedService{}).ApplyDNSDomain(plan, "rev-other")
	require.NoError(t, err)
	require.Equal(t, DomainStatusStalePlan, report.Status)
}
