package parser

import (
	"context"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
)

// subscriptionParsers are tried in order until one recognises the document. The order is part of
// the behaviour: the native JSON reader is exact, the Clash/SIP008 readers cover the other
// documented formats, and the raw/base64 list is the fallback that accepts what the others refused.
var subscriptionParsers = []func(ctx context.Context, content string) ([]option.Outbound, []option.Endpoint, error){
	ParseBoxSubscription,
	ParseClashSubscription,
	ParseSIP008Subscription,
	ParseRawSubscription,
}

// ParseSubscription decodes a subscription document and returns the outbounds it declares.
//
// # What is deliberately NOT returned
//
// Endpoints. A subscription may carry `endpoints` (WireGuard, Tailscale), and the format parsers
// keep them, but the first stage of the provider feature creates OUTBOUNDS only: an endpoint owns a
// tunnel, its own interface and a platform handover, which is a different lifecycle and a different
// admission decision. Dropping them here is explicit rather than accidental; provider endpoints are
// a separate, reviewed step.
//
// # Why the detour rewrite happens here
//
// A subscription node may name a SIBLING node as its `detour` (a chain built inside one
// subscription). The provider gives every node a new, provider-scoped tag, so the reference must
// move with it; a detour that names something outside the subscription is cleared, because the
// provider cannot know whether the main configuration still has that tag and a dangling dependency
// would make the whole node fail to create.
func ParseSubscription(ctx context.Context, content string, overrideDialerOptions *option.OverrideDialerOptions, providerTag string) ([]option.Outbound, error) {
	var parseErr error
	for _, parse := range subscriptionParsers {
		outbounds, _, err := parse(ctx, content)
		if len(outbounds) > 0 {
			return overrideOutbounds(outbounds, overrideDialerOptions, providerTag), nil
		}
		if err != nil {
			parseErr = E.Errors(parseErr, err)
		}
	}
	// A document none of the readers recognised is an ERROR, never an empty success. The caller
	// applies a successful parse as the provider's new node set, so returning (nil, nil) here would
	// turn a garbage body - "null", an HTML login page, an unsupported format - into "this provider
	// now has zero nodes" and wipe the last known good set.
	if parseErr == nil {
		return nil, E.New("no servers found: the document is not a recognised subscription format")
	}
	return nil, E.Cause(parseErr, "no servers found")
}

func providerTags(outbounds []option.Outbound) []string {
	tags := make([]string, 0, len(outbounds))
	for _, outbound := range outbounds {
		tags = append(tags, outbound.Tag)
	}
	return tags
}

// overrideOutbounds rewrites the references a subscription makes into the provider's namespace and
// applies the provider-level dialer override.
//
// It is generic over option.DialerOptionsWrapper rather than a switch over every outbound type: the
// wrapper is the declaration each options struct already makes, so a new protocol inherits the
// rewrite without a second list that can drift.
func overrideOutbounds(outbounds []option.Outbound, overrideDialerOptions *option.OverrideDialerOptions, providerTag string) []option.Outbound {
	tags := providerTags(outbounds)
	for i := range outbounds {
		wrapper, isWrapper := outbounds[i].Options.(option.DialerOptionsWrapper)
		if !isWrapper {
			continue
		}
		dialerOptions := wrapper.TakeDialerOptions()
		wrapper.ReplaceDialerOptions(overrideDialerOption(dialerOptions, overrideDialerOptions, tags, providerTag))
	}
	return outbounds
}

func overrideDialerOption(options option.DialerOptions, overrideDialerOptions *option.OverrideDialerOptions, tags []string, providerTag string) option.DialerOptions {
	if options.Detour != "" {
		if common.Contains(tags, options.Detour) {
			if providerTag != "" {
				options.Detour = providerTag + "/" + options.Detour
			}
		} else {
			options.Detour = ""
		}
	}
	if overrideDialerOptions == nil {
		return options
	}
	if overrideDialerOptions.Detour != nil && options.Detour == "" {
		options.Detour = *overrideDialerOptions.Detour
	}
	if overrideDialerOptions.BindInterface != nil {
		options.BindInterface = *overrideDialerOptions.BindInterface
	}
	if overrideDialerOptions.Inet4BindAddress != nil {
		options.Inet4BindAddress = overrideDialerOptions.Inet4BindAddress
	}
	if overrideDialerOptions.Inet6BindAddress != nil {
		options.Inet6BindAddress = overrideDialerOptions.Inet6BindAddress
	}
	if overrideDialerOptions.ProtectPath != nil {
		options.ProtectPath = *overrideDialerOptions.ProtectPath
	}
	if overrideDialerOptions.RoutingMark != nil {
		options.RoutingMark = *overrideDialerOptions.RoutingMark
	}
	if overrideDialerOptions.ReuseAddr != nil {
		options.ReuseAddr = *overrideDialerOptions.ReuseAddr
	}
	if overrideDialerOptions.ConnectTimeout != nil {
		options.ConnectTimeout = *overrideDialerOptions.ConnectTimeout
	}
	if overrideDialerOptions.TCPFastOpen != nil {
		options.TCPFastOpen = *overrideDialerOptions.TCPFastOpen
	}
	if overrideDialerOptions.TCPMultiPath != nil {
		options.TCPMultiPath = *overrideDialerOptions.TCPMultiPath
	}
	if overrideDialerOptions.TCPKeepAlive != nil {
		options.TCPKeepAlive = *overrideDialerOptions.TCPKeepAlive
	}
	if overrideDialerOptions.TCPKeepAliveInterval != nil {
		options.TCPKeepAliveInterval = *overrideDialerOptions.TCPKeepAliveInterval
	}
	if overrideDialerOptions.UDPFragment != nil {
		options.UDPFragment = overrideDialerOptions.UDPFragment
	}
	if overrideDialerOptions.DomainResolver != nil {
		options.DomainResolver = overrideDialerOptions.DomainResolver
	}
	if overrideDialerOptions.NetworkStrategy != nil {
		options.NetworkStrategy = overrideDialerOptions.NetworkStrategy
	}
	if overrideDialerOptions.NetworkType != nil {
		options.NetworkType = *overrideDialerOptions.NetworkType
	}
	if overrideDialerOptions.FallbackNetworkType != nil {
		options.FallbackNetworkType = *overrideDialerOptions.FallbackNetworkType
	}
	if overrideDialerOptions.FallbackDelay != nil {
		options.FallbackDelay = *overrideDialerOptions.FallbackDelay
	}
	if overrideDialerOptions.DisableTCPKeepAlive != nil {
		options.DisableTCPKeepAlive = *overrideDialerOptions.DisableTCPKeepAlive
	}
	//nolint:staticcheck
	if overrideDialerOptions.DomainStrategy != nil {
		options.DomainStrategy = *overrideDialerOptions.DomainStrategy
	}
	return options
}
