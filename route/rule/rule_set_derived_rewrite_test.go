//go:build unix

package rule

import (
	"bytes"
	"os"
	"testing"

	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// TestDerivedPublishRejectsRewriteAfterBuild is the repository regression for D-RS-02: the SRSM
// temp file is rewritten after the build and before publish. The manifest now carries the digest
// measured on the written file, so the artifact store must refuse the commit instead of storing B
// under A's source identity.
func TestDerivedPublishRejectsRewriteAfterBuild(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	logger := log.NewNOPFactory().Logger()
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")
	digestA := ruleSetDigest(payloadA)
	tag := "d-derived-rewrite-regression"
	sourceURL := "https://rules.example/d-derived-rewrite.srs"

	box := newRemoteDerivedTestRuleSet(t, ctx, tag, sourceURL, C.RuleSetFormatBinary)
	box.openArtifactStore()
	require.NotNil(t, box.derivedCache)

	ruleSetA, err := srs.Read(bytes.NewReader(payloadA), false)
	require.NoError(t, err)
	fileA, srsmDigest, srsmSize, ok := writeRuleSetMmapFile(ctx, logger, tag, ruleSetA)
	require.True(t, ok)
	defer fileA.Close()
	ruleSetB, err := srs.Read(bytes.NewReader(payloadB), false)
	require.NoError(t, err)
	fileB, _, _, ok := writeRuleSetMmapFile(ctx, logger, tag, ruleSetB)
	require.True(t, ok)
	defer fileB.Close()
	foreignSRSM, err := os.ReadFile(fileB.Name())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(fileA.Name(), foreignSRSM, 0o600))

	sourceA := box.remoteDerivedIdentity(digestA, int64(len(payloadA)))
	require.NotNil(t, sourceA)
	box.derivedCache.publish(sourceA, ruleSetA.Version, srsmDigest, srsmSize, fileA)
	require.Empty(t, remoteDerivedTestStoreDerivedVersions(t, base),
		"a rewritten temp file must not be published under the original digest")
	_ = box.Close()
}
