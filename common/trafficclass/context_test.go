package trafficclass

import (
	"context"
	"testing"
)

func TestClassTravelsInTheContext(t *testing.T) {
	t.Parallel()
	for _, class := range []Class{ClassDefault, ClassInteractive, ClassBulk, ClassRealtime} {
		got, loaded := ClassFromContext(WithClass(context.Background(), class))
		if !loaded || got != class {
			t.Fatalf("class %v: got %v, loaded %v", class, got, loaded)
		}
	}
}

func TestNoClassIsDistinguishableFromDefault(t *testing.T) {
	t.Parallel()
	// "nobody classified this flow" and "this flow was classified default" are different facts;
	// the admission policy treats them the same today, but a diagnostic that conflated them would
	// print a classification that never happened.
	_, loaded := ClassFromContext(context.Background())
	if loaded {
		t.Fatal("a bare context reported a class")
	}
	got, loaded := ClassFromContext(WithClass(context.Background(), ClassDefault))
	if !loaded || got != ClassDefault {
		t.Fatalf("an explicit default was not reported: %v, loaded %v", got, loaded)
	}
}

func TestOnlyInteractiveAndRealtimeAreHighPriority(t *testing.T) {
	t.Parallel()
	// This is the predicate the admission reserve is keyed on; if it ever returned true for
	// default or bulk, every flow could claim the reserve and the priority would mean nothing.
	if !ClassInteractive.IsHighPriority() || !ClassRealtime.IsHighPriority() {
		t.Fatal("an interactive class is not high priority")
	}
	if ClassDefault.IsHighPriority() || ClassBulk.IsHighPriority() {
		t.Fatal("a background class claims priority")
	}
}
