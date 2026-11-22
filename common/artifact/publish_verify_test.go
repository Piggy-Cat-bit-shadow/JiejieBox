package artifact_test

// Repository regression for D-RS-01: Store.publishVersion must refuse a staged artifact whose
// bytes were rewritten after Stage hashed them, and must not leave a version named by the old
// digest. The independent reviewer's probe is checked in so the window has a normal CI home.

import (
	"bytes"
	"os"
	"testing"

	"github.com/sagernet/sing-box/common/artifact"
	"github.com/stretchr/testify/require"
)

func TestPublishRefusesBytesRewrittenAfterStage(t *testing.T) {
	store, err := artifact.Open(t.TempDir())
	require.NoError(t, err)
	payloadA := bytes.Repeat([]byte("A"), 4096)
	payloadB := bytes.Repeat([]byte("B"), 4096)
	staged, err := store.Stage("key", artifact.NewManifest("schema", "compiler", "policy"), bytes.NewReader(payloadA), 0)
	require.NoError(t, err)
	require.Equal(t, len(payloadA), int(staged.Manifest().Size))
	require.NoError(t, os.WriteFile(staged.Path(), payloadB, 0o600))

	_, err = staged.Publish()
	require.Error(t, err, "publish must refuse bytes that no longer match the staged digest")

	_, err = store.Load("key")
	require.Error(t, err, "a refused publish must not leave a loadable version")

	version, err := store.Commit("key", artifact.NewManifest("schema", "compiler", "policy"), bytes.NewReader(payloadA))
	require.NoError(t, err)
	file, err := version.Open()
	require.NoError(t, err)
	read := make([]byte, len(payloadA))
	_, err = file.Read(read)
	require.NoError(t, err)
	require.Equal(t, payloadA, read)
	require.NoError(t, file.Close())
	version.Release()
}
