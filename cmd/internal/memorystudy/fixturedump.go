package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// dumpFixtureFiles writes the documents a case would have measured, and exits.
//
// # Why the documents are checked in as files at all
//
// The generator is the source of truth, but a reader auditing the ledger needs to see the exact
// bytes that were parsed: the protocol mix, the TLS blocks, the provider's separate node document,
// and the group's `providers` membership are all claims the report makes about the fixture. A
// regenerated-at-read-time fixture would let those claims drift from what ran. The files under
// fixtures/ are produced by this command and the report names the command that regenerates them.
//
// The port is zero: the document is a fixture, not a listening configuration, and a dump must not
// look like one that could be started.
func dumpFixtureFiles(spec runSpec, directory string) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	providerPath := ""
	if spec.ConfigSource == "provider" {
		// The configuration embeds the path a provider would read, so the dump keeps the two files
		// named by a stable relative path rather than by a temporary directory.
		providerPath = filepath.ToSlash(filepath.Join("fixtures", "nodes.json"))
	}
	fixture := generateFixtureWithProvider(spec, 0, "https://probe.memory-study.invalid/generate_204", providerPath)
	configPath := filepath.Join(directory, "config.json")
	if err := os.WriteFile(configPath, fixture.Document, 0o644); err != nil {
		return err
	}
	if spec.ConfigSource == "provider" {
		if err := os.WriteFile(filepath.Join(directory, "nodes.json"), fixture.ProviderDocument, 0o644); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stdout, "wrote %s (%d bytes, %d nodes, %d groups, %d member edges, source=%s)\n",
		configPath, len(fixture.Document), fixture.Nodes, fixture.Groups, fixture.MemberEdges, spec.ConfigSource)
	return nil
}
