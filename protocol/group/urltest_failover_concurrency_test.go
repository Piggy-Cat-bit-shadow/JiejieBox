package group

import (
	"context"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/common/urltest"
)

// TestURLTestGroupFailoverInstallAndSnapshotAreConcurrentSafe pins the pointer boundary: the
// configuration maps a policy once, while a diagnostics sampler may snapshot the group at the same
// moment. The pointer is replaced under the group's access lock and every reader loads it there;
// the FailoverState's own methods are internally locked.
func TestURLTestGroupFailoverInstallAndSnapshotAreConcurrentSafe(t *testing.T) {
	group := &URLTestGroup{ctx: context.Background()}

	const iterations = 1000
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	go func() {
		defer waitGroup.Done()
		for index := 0; index < iterations; index++ {
			group.ApplyFailoverPolicy(urltest.FailoverPolicy{})
		}
	}()
	go func() {
		defer waitGroup.Done()
		for index := 0; index < iterations; index++ {
			_ = group.HealthSnapshot()
		}
	}()
	waitGroup.Wait()
}
