package libbox

// Q21 — the document's own OBJECT and REFERENCE graph, and the build capability it is read in.
//
// # What the earlier account was, and what was missing
//
// AnalyzeConfig used to report how MANY objects a document declares (inbounds, outbounds,
// rule_sets, ...) plus a short, hand-written list of reference checks (route.final, dns.final,
// group members, rule_set tags). Two things were therefore unavailable to a client:
//
//   - which objects exist, of what type, under which tag - the identity a reference names;
//   - which object points at which other object, for every reference the option schema declares -
//     a detour, a domain_resolver server, a download_detour, a rule action's server, a rule's
//     inbound condition. The hand-written list simply did not know about them.
//
// "Write a coverage account" is not the same as "project the document", and the review said so.
// This file projects the graph itself.
//
// # Where the facts come from
//
// NOT from a second table maintained here. The option schema already declares every reference it
// has, as a struct tag: `reference:"outbound"`, `reference:"dns_server"`, `reference:"rule_set"`, ...
// The decoder is not the only consumer of that tag - a walk of the DECODED document can read it,
// which is what this file does. Consequences:
//
//   - a new reference field in the option schema is projected the moment it is declared, and this
//     file does not have to be taught about it;
//   - the projection shows the values the decoder produced, so a value it normalised (an empty
//     string, a list) is what the report shows;
//   - nothing here recomputes a constructor default. Effective values remain the four load-bearing
//     ones analyseEffective documents, and the runtime side stays pending (Checks.RuntimeChecked).
//
// # What a STATE means, and why there are four
//
// A reference is resolved against the tags the document declares. The four answers are different
// facts, and collapsing them would either invent a failure or hide one:
//
//   - `resolved`: the document declares the target.
//   - `pending`: the target is materialised at start by a provider fetch. The document cannot name
//     it, so it is neither resolved nor missing - this is the same rule that keeps a remote
//     provider's rendered members out of `findings`.
//   - `dangling`: nothing in the document can satisfy the target, and the core fails when it needs
//     it (at start, or when the rule or dial that names it runs). These are the entries `findings`
//     is built from, so the report's reference list and its findings are one projection read
//     twice instead of two lists that can disagree.
//   - `unresolved`: nothing in the document can satisfy the target, and the report does NOT claim a
//     failure: an inbound condition that names no declared inbound simply never matches, and a
//     network namespace may exist in the operating system without being declared here. Saying
//     `dangling` for these would be a false failure report; saying `resolved` would hide a dead
//     rule. The reason field says which of the two a client is looking at.
//
// The line between `dangling` and `unresolved` is a fact about the core's own resolution points,
// not a preference: the router refuses to start with a rule_set it never declared, a group resolves
// its members at start, and neither checks a rule's inbound condition against the declared inbounds.
//
// # The capability list
//
// `capabilities` answers "what can THIS BINARY construct", from the registries the same build
// registered - not from a table of type names in the analysis. A client that receives a report from
// a build it cannot otherwise identify can compare the document's types against it. Types the build
// refuses at CONSTRUCTION time (a removed protocol registered through a stub) are not visible here:
// the registry keeps them constructible so the decoder can produce the core's own error, and the
// analysis path does not construct anything to find out.
//
// # Bounds
//
// A document can name thousands of references (one per rule). The list is bounded
// (maxAnalysisReferences) and says so (referencesTruncated) rather than growing without limit: this
// report is produced on a phone, and a silently truncated list would be worse than a stated one.
// Objects are not bounded - they are exactly the objects the document declares.

import (
	"context"
	"reflect"
	"strconv"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"
)

// maxAnalysisReferences bounds the reference list of one analysis.
const maxAnalysisReferences = 512

// maxReferenceWalkDepth bounds the reflection walk. An option document is a finite tree a few
// levels deep; the bound only exists so a malformed hand-built value cannot recurse forever.
const maxReferenceWalkDepth = 32

// ConfigObject is one object the document declares.
//
// Kind is the namespace the object lives in (inbound, outbound, endpoint, service, provider,
// certificate_provider, dns_server, rule_set, network_namespace). Tag is the name a reference uses,
// and Type is what the document asked the registry for.
type ConfigObject struct {
	Kind string `json:"kind"`
	Tag  string `json:"tag,omitempty"`
	Type string `json:"type,omitempty"`
}

// ConfigReference is one reference the option schema declares, with the path it sits at.
//
// From is the document path of the FIELD (for a list, the element): `outbounds[1].detour`,
// `route.rules[0].action.server`, `route.final`. State is resolved / pending / dangling /
// unresolved; see the file comment for what each one is a fact about.
type ConfigReference struct {
	From   string `json:"from"`
	Kind   string `json:"kind"`
	Tag    string `json:"tag"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// ConfigCapability lists the types one registry of THIS BUILD can construct.
type ConfigCapability struct {
	Kind  string   `json:"kind"`
	Types []string `json:"types"`
}

// Object kinds. They are the namespaces a tag lives in, and the values a reference's `kind` takes.
const (
	objectKindInbound             = "inbound"
	objectKindOutbound            = "outbound"
	objectKindEndpoint            = "endpoint"
	objectKindService             = "service"
	objectKindProvider            = "provider"
	objectKindCertificateProvider = "certificate_provider"
	objectKindDNSServer           = "dns_server"
	objectKindRuleSet             = "rule_set"
	objectKindNetworkNamespace    = "network_namespace"
)

// Reference states. See the file comment.
const (
	referenceStateResolved   = "resolved"
	referenceStatePending    = "pending"
	referenceStateDangling   = "dangling"
	referenceStateUnresolved = "unresolved"
)

// Reason texts. Each one names the moment the core would fail, because "missing" alone does not
// tell a client whether the profile is broken or merely has a rule that can never match.
const (
	referenceReasonOutboundMissing = "no outbound or endpoint with this tag is declared, so the core cannot resolve it"
	referenceReasonProviderMember  = "a provider renders this member at start, so the document cannot declare it"
	referenceReasonDNSServer       = "no DNS server with this tag is declared, so the core cannot resolve it"
	referenceReasonRuleSet         = "the box fails to start with: rule-set not found: "
	referenceReasonProvider        = "no provider with this tag is declared, so the group cannot load its members"
	referenceReasonInbound         = "no inbound with this tag is declared, so a condition or hand-off naming it " +
		"never applies; the box still starts"
	referenceReasonNetworkNamespace = "the document declares no such namespace; the name is resolved by the " +
		"operating system when a socket is created, which this analysis cannot observe"
	referenceReasonUnknownKind = "this build does not know what resolving this reference would mean"
)

// objectGraph is what one walk of the decoded document produced.
type objectGraph struct {
	Objects      []ConfigObject
	References   []ConfigReference
	Truncated    bool
	Findings     []ConfigFinding
	Capabilities []ConfigCapability
}

// analyseObjectGraph projects the document's objects and references, derives the dangling-reference
// findings from that same projection, and lists this build's constructible types.
func analyseObjectGraph(ctx context.Context, options *option.Options) objectGraph {
	var graph objectGraph
	objects, declaredTags, objectTypes := collectDeclaredObjects(options)
	graph.Objects = make([]ConfigObject, 0, len(objects))
	for _, object := range objects {
		graph.Objects = append(graph.Objects, object.ConfigObject)
	}
	fields := collectDocumentReferences(options)
	if len(fields) > maxAnalysisReferences {
		graph.Truncated = true
		fields = fields[:maxAnalysisReferences]
	}
	graph.References = make([]ConfigReference, 0, len(fields))
	for _, field := range fields {
		state, reason := classifyReference(field.kind, field.tag, declaredTags)
		graph.References = append(graph.References, ConfigReference{
			From:   field.path,
			Kind:   field.kind,
			Tag:    field.tag,
			State:  state,
			Reason: reason,
		})
		if state != referenceStateDangling {
			continue
		}
		graph.Findings = append(graph.Findings, danglingFinding(field, objectTypes))
	}
	graph.Capabilities = analyseCapabilities(ctx)
	return graph
}

// danglingFinding renders one dangling reference as a finding.
//
// The detail strings for the cases the report already carried are kept verbatim ("group member x",
// "route.final x", "route.rules[0] rule_set x"): a client that keys on them must not have to change
// because the projection behind them became complete.
func danglingFinding(field referenceField, objectTypes map[string]string) ConfigFinding {
	switch field.kind {
	case objectKindRuleSet:
		return ConfigFinding{
			Code:   findingDanglingReference,
			Detail: referenceOwnerPath(field.path) + " rule_set " + field.tag,
			Reason: referenceReasonRuleSet + field.tag,
		}
	}
	if owner, fieldName := splitReferencePath(field.path); groupKinds[objectTypes[owner]] {
		switch fieldName {
		case "outbounds":
			return ConfigFinding{Code: findingDanglingReference, Detail: "group member " + field.tag}
		case "providers":
			return ConfigFinding{Code: findingDanglingReference, Detail: "group provider " + field.tag}
		}
	}
	if field.path == "route.final" || field.path == "dns.final" {
		return ConfigFinding{Code: findingDanglingReference, Detail: field.path + " " + field.tag}
	}
	return ConfigFinding{
		Code:   findingDanglingReference,
		Detail: field.path + " " + field.kind + " " + field.tag,
	}
}

// groupKinds are the outbound types whose member list is a reference list.
var groupKinds = map[string]bool{
	C.TypeSelector:    true,
	C.TypeURLTest:     true,
	C.TypeLoadBalance: true,
}

// splitReferencePath separates the path of the owning object from the field inside it:
// "outbounds[1].outbounds[0]" -> ("outbounds[1]", "outbounds").
func splitReferencePath(path string) (string, string) {
	dot := strings.LastIndex(path, ".")
	if dot < 0 {
		return path, ""
	}
	field := path[dot+1:]
	if bracket := strings.Index(field, "["); bracket >= 0 {
		field = field[:bracket]
	}
	return path[:dot], field
}

// referenceOwnerPath drops the trailing field segment, keeping any indices of the owner:
// "route.rules[0].rule_set[1]" -> "route.rules[0]".
func referenceOwnerPath(path string) string {
	owner, _ := splitReferencePath(path)
	return owner
}

// declaredObject pairs an object with the document path it was declared at.
type declaredObject struct {
	path string
	ConfigObject
}

// collectDeclaredObjects lists every object the document declares, and the tag sets a reference is
// resolved against.
func collectDeclaredObjects(options *option.Options) (objects []declaredObject, tags map[string]map[string]bool, objectTypes map[string]string) {
	tags = make(map[string]map[string]bool)
	objectTypes = make(map[string]string)
	addObject := func(kind string, path string, tag string, objectType string) {
		if tag != "" {
			if tags[kind] == nil {
				tags[kind] = make(map[string]bool)
			}
			tags[kind][tag] = true
		}
		objectTypes[path] = objectType
		objects = append(objects, declaredObject{
			path:         path,
			ConfigObject: ConfigObject{Kind: kind, Tag: tag, Type: objectType},
		})
	}
	for index, inbound := range options.Inbounds {
		addObject(objectKindInbound, "inbounds["+strconv.Itoa(index)+"]", inbound.Tag, inbound.Type)
	}
	for index, outbound := range options.Outbounds {
		addObject(objectKindOutbound, "outbounds["+strconv.Itoa(index)+"]", outbound.Tag, outbound.Type)
	}
	for index, endpoint := range options.Endpoints {
		addObject(objectKindEndpoint, "endpoints["+strconv.Itoa(index)+"]", endpoint.Tag, endpoint.Type)
	}
	for index, service := range options.Services {
		addObject(objectKindService, "services["+strconv.Itoa(index)+"]", service.Tag, service.Type)
	}
	for index, provider := range options.Providers {
		addObject(objectKindProvider, "providers["+strconv.Itoa(index)+"]", provider.Tag, provider.Type)
	}
	for index, certificateProvider := range options.CertificateProviders {
		addObject(objectKindCertificateProvider,
			"certificate_providers["+strconv.Itoa(index)+"]", certificateProvider.Tag, certificateProvider.Type)
	}
	if options.DNS != nil {
		for index, server := range options.DNS.Servers {
			addObject(objectKindDNSServer, "dns.servers["+strconv.Itoa(index)+"]", server.Tag, server.Type)
		}
	}
	if options.Route != nil {
		for index, ruleSet := range options.Route.RuleSet {
			// A rule set may carry several tags; each one is a name a reference can use, so each one
			// is an object entry. The type is the same for all of them.
			if len(ruleSet.Tag) == 0 {
				addObject(objectKindRuleSet, "route.rule_set["+strconv.Itoa(index)+"]", "", ruleSet.Type)
				continue
			}
			for tagIndex, tag := range ruleSet.Tag {
				addObject(objectKindRuleSet,
					"route.rule_set["+strconv.Itoa(index)+"].tag["+strconv.Itoa(tagIndex)+"]", tag, ruleSet.Type)
			}
		}
	}
	for index, namespace := range options.NetworkNamespaces {
		addObject(objectKindNetworkNamespace, "network_namespaces["+strconv.Itoa(index)+"]", namespace.Tag, namespace.Type)
	}
	return objects, tags, objectTypes
}

// referenceField is one reference-tagged field found in the decoded document.
type referenceField struct {
	path string
	kind string
	tag  string
}

// collectDocumentReferences walks the decoded document and returns every reference the option
// schema declares, in document order.
func collectDocumentReferences(options *option.Options) []referenceField {
	var fields []referenceField
	walkReferenceFields("", reflect.ValueOf(options), &fields, 0)
	return fields
}

// walkReferenceFields recurses through the decoded value, building the JSON path as it goes.
func walkReferenceFields(prefix string, value reflect.Value, fields *[]referenceField, depth int) {
	if depth > maxReferenceWalkDepth || !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return
		}
		walkReferenceFields(prefix, value.Elem(), fields, depth+1)
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && value.IsNil() {
			return
		}
		for index := 0; index < value.Len(); index++ {
			walkReferenceFields(prefix+"["+strconv.Itoa(index)+"]", value.Index(index), fields, depth+1)
		}
	case reflect.Struct:
		valueType := value.Type()
		for index := 0; index < value.NumField(); index++ {
			field := valueType.Field(index)
			if field.PkgPath != "" {
				// An unexported field is not part of the document.
				continue
			}
			fieldValue := value.Field(index)
			fieldPath := prefix
			if !field.Anonymous {
				name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
				if name == "-" {
					// The document does not NAME this field, but its contents are still part of the
					// decoded value: the typed body of an object (`Options`), a rule's default or
					// logical half, and a rule set's per-type body all live under a `json:"-"` field
					// and are written by that type's own marshaller. Skipping them would skip most
					// of the document, so they are walked at the owner's path.
					walkReferenceFields(prefix, fieldValue, fields, depth+1)
					continue
				}
				if name == "" {
					name = field.Name
				}
				fieldPath = joinReferencePath(prefix, name)
			}
			if referenceKind := field.Tag.Get("reference"); referenceKind != "" {
				appendReferenceTags(fieldPath, referenceKind, fieldValue, fields)
				continue
			}
			walkReferenceFields(fieldPath, fieldValue, fields, depth+1)
		}
	}
}

// appendReferenceTags records the non-empty tags a reference field holds. A reference field is a
// string, a pointer to one, or a list of them; anything else is a schema change this build does not
// know how to read, and it is skipped rather than guessed at.
func appendReferenceTags(path string, kind string, value reflect.Value, fields *[]referenceField) {
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return
		}
		appendReferenceTags(path, kind, value.Elem(), fields)
	case reflect.String:
		if tag := value.String(); tag != "" {
			*fields = append(*fields, referenceField{path: path, kind: kind, tag: tag})
		}
	case reflect.Slice, reflect.Array:
		for index := 0; index < value.Len(); index++ {
			appendReferenceTags(path+"["+strconv.Itoa(index)+"]", kind, value.Index(index), fields)
		}
	}
}

// joinReferencePath appends one JSON field name to a document path.
func joinReferencePath(prefix string, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// classifyReference resolves one reference against the tags the document declares.
func classifyReference(kind string, tag string, declared map[string]map[string]bool) (state string, reason string) {
	switch kind {
	case objectKindOutbound:
		// An endpoint is a route target too: a group member or a detour may name one.
		if declared[objectKindOutbound][tag] || declared[objectKindEndpoint][tag] {
			return referenceStateResolved, ""
		}
		if strings.Contains(tag, "/") {
			return referenceStatePending, referenceReasonProviderMember
		}
		return referenceStateDangling, referenceReasonOutboundMissing
	case objectKindDNSServer:
		if declared[objectKindDNSServer][tag] {
			return referenceStateResolved, ""
		}
		return referenceStateDangling, referenceReasonDNSServer
	case objectKindRuleSet:
		if declared[objectKindRuleSet][tag] {
			return referenceStateResolved, ""
		}
		return referenceStateDangling, referenceReasonRuleSet + tag
	case objectKindProvider:
		if declared[objectKindProvider][tag] {
			return referenceStateResolved, ""
		}
		return referenceStateDangling, referenceReasonProvider
	case objectKindInbound:
		if declared[objectKindInbound][tag] {
			return referenceStateResolved, ""
		}
		return referenceStateUnresolved, referenceReasonInbound
	case objectKindNetworkNamespace:
		if declared[objectKindNetworkNamespace][tag] {
			return referenceStateResolved, ""
		}
		return referenceStateUnresolved, referenceReasonNetworkNamespace
	default:
		if declared[kind][tag] {
			return referenceStateResolved, ""
		}
		return referenceStateUnresolved, referenceReasonUnknownKind
	}
}

// analyseCapabilities reports the types each registry of this build can construct.
//
// It asks the SAME registries the decoder used (service.FromContext, populated by baseContext), so
// the list cannot drift from what the build actually accepts. A registry that is absent from the
// context contributes nothing rather than an empty claim.
func analyseCapabilities(ctx context.Context) []ConfigCapability {
	capabilities := make([]ConfigCapability, 0, 7)
	add := func(kind string, optionTypes []string) {
		if len(optionTypes) == 0 {
			return
		}
		capabilities = append(capabilities, ConfigCapability{Kind: kind, Types: optionTypes})
	}
	if registry := service.FromContext[option.InboundOptionsRegistry](ctx); registry != nil {
		add(objectKindInbound, registry.OptionTypes())
	}
	if registry := service.FromContext[option.OutboundOptionsRegistry](ctx); registry != nil {
		add(objectKindOutbound, registry.OptionTypes())
	}
	if registry := service.FromContext[option.EndpointOptionsRegistry](ctx); registry != nil {
		add(objectKindEndpoint, registry.OptionTypes())
	}
	if registry := service.FromContext[option.ServiceOptionsRegistry](ctx); registry != nil {
		add(objectKindService, registry.OptionTypes())
	}
	if registry := service.FromContext[option.ProviderOptionsRegistry](ctx); registry != nil {
		add(objectKindProvider, registry.OptionTypes())
	}
	if registry := service.FromContext[option.DNSTransportOptionsRegistry](ctx); registry != nil {
		add(objectKindDNSServer, registry.OptionTypes())
	}
	if registry := service.FromContext[option.CertificateProviderOptionsRegistry](ctx); registry != nil {
		add(objectKindCertificateProvider, registry.OptionTypes())
	}
	return capabilities
}
