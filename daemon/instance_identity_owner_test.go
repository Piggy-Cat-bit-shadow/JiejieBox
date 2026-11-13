package daemon

// One writer for (Instance, instanceID).
//
// A connection-event batch names the instance that produced it, and `connectionStreamBinding` refuses
// to serve a stream when that identity is empty. The pair is therefore ONE value: `setInstanceLocked`
// writes both, under `serviceAccess`, and everything that publishes an instance goes through it.
//
// The 0.1.45 macOS gate failed on exactly this: `NewAttachedService` - the Native API service of a
// standalone `run`, which the dashboard's connection list and the headless smoke test both use -
// assigned `s.instance` directly. Status, logs and outbounds streamed normally, and every
// connection-event subscription was refused with "service is not running" because the identity it
// read alongside the instance was empty. Nothing in the Go unit suite noticed, because the identity
// tests construct the started service and set both fields themselves.
//
// This is a structural test on purpose. A behavioural one would need a context with every manager the
// instance attaches to, and the defect is not in a behaviour these files OWN: it is one line that
// bypassed the owner. Reading the package's own source is the smallest check that fails when a new
// constructor repeats the mistake.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// identityWriterFile is the file that owns the pair; inside it the assignment is the writer itself.
const identityWriterFile = "started_service.go"

func TestTheInstanceIdentityHasExactlyOneWriter(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err, "the package directory must be readable")

	parsed := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed++
		if name == identityWriterFile {
			continue
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, name, nil, 0)
		require.NoError(t, err, "every production file in this package must parse: %s", name)
		ast.Inspect(file, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				selector, ok := lhs.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "instance" {
					continue
				}
				receiver, ok := selector.X.(*ast.Ident)
				if !ok || receiver.Name != "s" {
					continue
				}
				position := fileSet.Position(assign.Pos())
				t.Errorf(
					"%s:%d assigns s.instance directly.\n"+
						"Publish it with setInstanceLocked under serviceAccess instead: the instance and the\n"+
						"identity a connection-event stream reports are written together, and a stream that\n"+
						"cannot name its instance is refused. See %s.",
					name, position.Line, identityWriterFile,
				)
			}
			return true
		})
	}
	require.NotZero(t, parsed, "the scan found no production files; it would pass vacuously")
}
