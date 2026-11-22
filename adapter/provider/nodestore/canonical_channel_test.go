package nodestore

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file is the formal regression for the independent review's F3 channel counterexample:
// `typeKey` rendered every channel type as "chan " + element type and dropped the direction, so a nil
// `chan int` and a nil `<-chan int` produced identical records and `Equivalent` reported true while
// `reflect.DeepEqual` reported false (the value types are different, and DeepEqual starts by
// comparing types). The type key now carries the direction, so the three channel types are three
// distinct identities.

// channelAnyHolder forces the channel type through an interface value, where the dynamic type key is
// written into the record. The named-struct path only writes the enclosing type's name, so an
// interface field is the shape that actually exercises the dynamic type key at run time.
type channelAnyHolder struct {
	Any any
}

// TestChannelDirectionIsTypeIdentity is the review's repro: the three channel directions must be
// three different types, and nil values of different directions must never compare equal.
func TestChannelDirectionIsTypeIdentity(t *testing.T) {
	var both chan int
	var receives <-chan int
	var sends chan<- int

	// DeepEqual compares types first, so every cross-direction pair is different even when both
	// values are nil. Same-direction nil values ARE deeply equal.
	require.False(t, reflect.DeepEqual(both, receives))
	require.False(t, reflect.DeepEqual(both, sends))
	require.False(t, reflect.DeepEqual(receives, sends))
	require.False(t, Equivalent(both, receives))
	require.False(t, Equivalent(both, sends))
	require.False(t, Equivalent(receives, sends))

	require.True(t, reflect.DeepEqual(both, (chan int)(nil)))
	require.True(t, Equivalent(both, (chan int)(nil)))
	require.True(t, Equivalent(receives, (<-chan int)(nil)))
	require.True(t, Equivalent(sends, (chan<- int)(nil)))

	// The type key is where the direction now lives.
	require.NotEqual(t, typeKey(reflect.TypeOf(both)), typeKey(reflect.TypeOf(receives)))
	require.NotEqual(t, typeKey(reflect.TypeOf(both)), typeKey(reflect.TypeOf(sends)))
	require.NotEqual(t, typeKey(reflect.TypeOf(receives)), typeKey(reflect.TypeOf(sends)))

	// Through an interface value, the record writes the dynamic type key; before the fix all three
	// holders encoded to identical bytes.
	require.False(t, Equivalent(channelAnyHolder{Any: both}, channelAnyHolder{Any: receives}))
	require.False(t, Equivalent(channelAnyHolder{Any: both}, channelAnyHolder{Any: sends}))
	require.False(t, Equivalent(channelAnyHolder{Any: receives}, channelAnyHolder{Any: sends}))
	require.True(t, Equivalent(channelAnyHolder{Any: both}, channelAnyHolder{Any: (chan int)(nil)}))
}

// TestChannelDirectionIsEnclosingTypeIdentity covers the shapes where the direction is implied by an
// enclosing type instead of by an interface value: struct fields, slice elements and map values. All
// of them are different Go types and must therefore be different records.
func TestChannelDirectionIsEnclosingTypeIdentity(t *testing.T) {
	type bothHolder struct{ Channel chan int }
	type receiveHolder struct{ Channel <-chan int }
	type sendHolder struct{ Channel chan<- int }

	require.False(t, Equivalent(bothHolder{}, receiveHolder{}))
	require.False(t, Equivalent(bothHolder{}, sendHolder{}))
	require.False(t, Equivalent(receiveHolder{}, sendHolder{}))

	require.False(t, Equivalent([]chan int(nil), []<-chan int(nil)))
	require.False(t, Equivalent(map[string]chan int(nil), map[string]<-chan int(nil)))
	require.False(t, Equivalent([1]chan<- int{}, [1]chan int{}))
}

// TestChannelNilAndIdentitySemantics keeps the documented conservative departure visible: a non-nil
// channel is encoded as a unique token because a stored record outlives the channel it came from, so
// two records of the same live channel are never equal even though DeepEqual has a same-object
// shortcut. That direction is safe (a rebuild), the direction this file guards is the false "equal".
func TestChannelNilAndIdentitySemantics(t *testing.T) {
	channel := make(chan int)
	require.True(t, reflect.DeepEqual(channel, channel))
	require.False(t, Equivalent(channel, channel), "a record outlives the channel, so identity cannot be proven")

	receiveView := (<-chan int)(channel)
	require.False(t, reflect.DeepEqual(channel, receiveView))
	require.False(t, Equivalent(channel, receiveView))

	require.False(t, Equivalent(channel, (chan int)(nil)))
	require.False(t, reflect.DeepEqual(channel, (chan int)(nil)))
	// A second, distinct non-nil channel of the same type is not deeply equal either.
	other := make(chan int)
	require.False(t, reflect.DeepEqual(channel, other))
	require.False(t, Equivalent(channel, other))
}
