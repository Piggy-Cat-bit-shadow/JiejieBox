package catalog

import (
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/stretchr/testify/require"
)

// TestPutDedupeCannotReturnReleasedRecord is the deterministic L-CAT-03 regression. The test hook
// parks Put after it has observed refs>0 and before Add(1), then starts the last Release. The
// fixed Release cannot decrement until Put leaves the lock, so Put's Add wins and the returned
// reference stays valid. The pre-fix Release decremented outside the lock, unlinked the record and
// cleared its payload, so this assertion fails.
func TestPutDedupeCannotReturnReleasedRecord(t *testing.T) {
	store := NewStore()
	payload := []byte("same-record")
	first, err := store.Put("owner", C.TypeSOCKS, payload)
	require.NoError(t, err)

	entered := make(chan struct{})
	resume := make(chan struct{})
	store.testDedupeBarrier = func() {
		close(entered)
		<-resume
	}
	defer func() { store.testDedupeBarrier = nil }()

	result := make(chan Ref, 1)
	resultErr := make(chan error, 1)
	go func() {
		other, putErr := store.Put("owner", C.TypeSOCKS, payload)
		if putErr != nil {
			resultErr <- putErr
			return
		}
		result <- other
	}()
	<-entered
	released := make(chan struct{})
	go func() {
		first.Release()
		close(released)
	}()
	close(resume)
	<-released
	var other Ref
	select {
	case other = <-result:
	case err = <-resultErr:
		t.Fatal(err)
	}
	require.True(t, other.Valid(), "Put returned a record that the last Release had killed")
	require.NotEmpty(t, other.Payload())
	other.Release()
	require.Equal(t, 0, store.Stats().Records)
}
