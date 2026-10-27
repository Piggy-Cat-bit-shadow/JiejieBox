package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/schema"

	"github.com/stretchr/testify/require"
)

// TestTrackedSchemaMatchesTheGenerator pins the generated artifact to its source.
//
// # The defect this exists for
//
// `docs/schema.json` is a tracked product artifact read outside this repository (the documentation
// site and an editor schema URL), and nothing regenerated or compared it. It had drifted: the
// `User` definition advertised `Username` and `Password` while the option structs carry
// `json:"username"` and `json:"password"`, so a user following the published schema wrote keys the
// parser does not accept. There is no way for a consumer to notice that from the artifact alone.
//
// # Why a test and not only the Makefile target
//
// `make schema` is a manual action and was not run in the commit that changed the structs. A test
// runs where the source is changed - in CI under the same canonical tags - so the next drift fails
// the change that introduces it instead of surviving until someone regenerates by hand.
//
// The comparison is exact. The generator sorts its definitions and marshals with a fixed indent, so
// a difference is either a real schema change or a formatting change that would otherwise be
// committed on the next manual run; both are what the tracked artifact is supposed to describe.
func TestTrackedSchemaMatchesTheGenerator(t *testing.T) {
	generated, err := schema.Generate(include.Context(context.Background()), reflect.TypeFor[option.Options]())
	require.NoError(t, err)

	trackedPath := filepath.Join("..", "..", "docs", "schema.json")
	tracked, err := os.ReadFile(trackedPath)
	require.NoError(t, err, "the tracked schema must exist at %s", trackedPath)

	require.Equal(t, string(tracked), string(generated),
		"docs/schema.json does not match option/schema.go: run `make schema` and commit the result")
}
