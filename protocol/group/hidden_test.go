package group

// `hidden` is a DISPLAY property, and these tests pin the half of it that lives in this package:
// the configuration value reaches the runtime object, for every group type that can carry it, and an
// absent field means visible.
//
// The other halves are pinned elsewhere on purpose, because they belong to other layers:
//
//	option/include  the key decodes on these three types and is REFUSED on a leaf outbound
//	daemon          the group list omits a hidden group while the outbound list keeps it
//
// Nothing here asserts that `hidden` changes a selection, a probe or a dial: it must not, and the
// existing group tests are the evidence for that. What a hidden group must keep is exercised end to
// end in daemon/group_hidden_display_test.go.

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// hiddenCapableGroup is one way to build a group that may declare itself hidden.
type hiddenCapableGroup struct {
	name  string
	build func(hidden bool) (adapter.Outbound, error)
}

// everyGroupTypeThatCanBeHidden is a hand-kept list of the PRODUCTION constructors, not a registry
// dump: this package cannot enumerate what include registers (include imports this package). Keeping
// it complete is therefore a review obligation, and the reverse direction IS checked mechanically -
// include/group_hidden_test.go walks the real outbound registry and asserts that the three types
// below are the ONLY ones whose options carry the field.
func everyGroupTypeThatCanBeHidden() []hiddenCapableGroup {
	return []hiddenCapableGroup{
		{
			name: "selector",
			build: func(hidden bool) (adapter.Outbound, error) {
				return NewSelector(context.Background(), nil, nil, "selector", option.SelectorOutboundOptions{
					Outbounds: []string{"member"},
					Hidden:    hidden,
				})
			},
		},
		{
			name: "urltest",
			build: func(hidden bool) (adapter.Outbound, error) {
				return NewURLTest(context.Background(), nil, nil, "urltest", option.URLTestOutboundOptions{
					Outbounds: []string{"member"},
					Hidden:    hidden,
				})
			},
		},
		{
			name: "loadbalance",
			build: func(hidden bool) (adapter.Outbound, error) {
				return NewLoadBalance(context.Background(), nil, nil, "loadbalance", option.LoadBalanceOutboundOptions{
					Outbounds: []string{"member"},
					Hidden:    hidden,
				})
			},
		},
	}
}

// TestEveryGroupTypeReportsTheHiddenSettingItWasBuiltWith is the whole Options -> runtime contract:
// true is reported, false is reported, and every group type that can be configured participates.
func TestEveryGroupTypeReportsTheHiddenSettingItWasBuiltWith(t *testing.T) {
	for _, groupType := range everyGroupTypeThatCanBeHidden() {
		t.Run(groupType.name, func(t *testing.T) {
			for _, hidden := range []bool{false, true} {
				created, err := groupType.build(hidden)
				require.NoError(t, err, "building a %s group must not depend on hidden", groupType.name)
				hiddenGroup, isHiddenCapable := created.(adapter.HiddenOutboundGroup)
				require.True(t, isHiddenCapable,
					"%s must implement adapter.HiddenOutboundGroup, or its option would be silently ignored",
					groupType.name)
				require.Equal(t, hidden, hiddenGroup.Hidden())
			}
		})
	}
}

// TestAnUnsetHiddenFieldMeansVisible pins the default at the place it is consumed.
//
// The zero value of the Go field is what the decoder leaves behind when the key is absent, so this
// test is the one that would fail if the field were ever changed to *bool: a nil pointer would need
// a decision here, and "absent" would stop being indistinguishable from an explicit false.
func TestAnUnsetHiddenFieldMeansVisible(t *testing.T) {
	for _, groupType := range everyGroupTypeThatCanBeHidden() {
		t.Run(groupType.name, func(t *testing.T) {
			created, err := groupType.build(false)
			require.NoError(t, err)
			hiddenGroup, isHiddenCapable := created.(adapter.HiddenOutboundGroup)
			require.True(t, isHiddenCapable)
			require.False(t, hiddenGroup.Hidden(), "a %s group that did not ask to be hidden is visible", groupType.name)
		})
	}
}
