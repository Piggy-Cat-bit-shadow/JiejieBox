package log

import (
	"context"
	"io"
	"testing"

	"github.com/sagernet/sing-box/option"
)

// newLevelTestFactory builds the factory exactly the way box.New does, with the
// requested document's log.level. io.Discard keeps the test from writing to the
// process stderr.
func newLevelTestFactory(t *testing.T, level string) Factory {
	t.Helper()
	factory, err := New(Options{
		Context:       context.Background(),
		Options:       option.LogOptions{Level: level},
		DefaultWriter: io.Discard,
	})
	if err != nil {
		t.Fatalf("create log factory for level %q: %v", level, err)
	}
	t.Cleanup(func() {
		_ = factory.Close()
	})
	return factory
}

// TestDefaultLogLevelIsInfo pins the product default. A document that does not
// declare log.level must not select trace or debug: that is both the operator's
// expectation and the difference between a quiet run and a debug-configured one.
func TestDefaultLogLevelIsInfo(t *testing.T) {
	factory := newLevelTestFactory(t, "")
	if got := factory.Level(); got != LevelInfo {
		t.Fatalf("unset log.level selected %s, want info", FormatLevel(got))
	}
}

// TestExplicitLogLevelsAreHonored is the other half of the default: every level an
// operator can name must still win over the default. This covers debug and trace
// explicitly because those are the diagnostic levels the default change must not
// remove.
func TestExplicitLogLevelsAreHonored(t *testing.T) {
	cases := map[string]Level{
		"trace":   LevelTrace,
		"debug":   LevelDebug,
		"info":    LevelInfo,
		"warn":    LevelWarn,
		"warning": LevelWarn,
		"error":   LevelError,
		"fatal":   LevelFatal,
		"panic":   LevelPanic,
	}
	for text, want := range cases {
		t.Run(text, func(t *testing.T) {
			factory := newLevelTestFactory(t, text)
			if got := factory.Level(); got != want {
				t.Fatalf("log.level %q selected %s, want %s", text, FormatLevel(got), FormatLevel(want))
			}
		})
	}
}

// TestUnknownLogLevelIsRejected pins the validation path: changing the default must
// not turn an invalid declared level into a silently accepted one.
func TestUnknownLogLevelIsRejected(t *testing.T) {
	_, err := New(Options{
		Context:       context.Background(),
		Options:       option.LogOptions{Level: "bogus"},
		DefaultWriter: io.Discard,
	})
	if err == nil {
		t.Fatal("log.New accepted an unknown log level")
	}
}
