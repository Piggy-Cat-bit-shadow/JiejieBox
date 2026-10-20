package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Reading a Chromium NetLog without hardcoding its event ids.
//
// # The failure this replaces
//
// The pool-isolation self-test counted `"type":249` in the raw file. Chromium 154 assigns
// HTTP2_SESSION_INITIALIZED the id 267, so the count was always zero and the invariant "three
// insecure pools open three HTTP/2 sessions" was never actually verified. Hardcoding 267 would only
// move the bug to the next Chromium roll, so the id is resolved from the file's own constants block
// at runtime.
//
// # Shape
//
// A NetLog file is one JSON object with a `constants` block and an `events` array:
//
//	{"constants": {"logEventTypes": {"HTTP2_SESSION_INITIALIZED": 267, ...}, ...},
//	 "events": [{"type": 267, "phase": 0, "source": {...}, "params": {...}}, ...]}
//
// The reader below streams the events array (NetLog files reach hundreds of megabytes) and counts
// events per type id; the id for the requested name is resolved from whichever constants sub-map
// contains it, so a renamed constants section does not break the count. Counting by id first and
// resolving afterwards also makes the result independent of whether `constants` appears before or
// after `events`.

// netLogMaxReportedKeys bounds the error message so a failure never dumps the whole constants block.
const netLogMaxReportedKeys = 12

// netLogEventCount returns how many events of the named type the file contains, and the numeric id
// that name resolved to.
func netLogEventCount(path string, eventName string) (count int, resolvedID int, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("open netlog %q: %w", path, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(bufio.NewReaderSize(file, 1<<20))
	opening, err := decoder.Token()
	if err != nil {
		return 0, 0, fmt.Errorf("decode netlog %q: %w", path, err)
	}
	if opening != json.Delim('{') {
		return 0, 0, fmt.Errorf("decode netlog %q: the file is not a JSON object", path)
	}

	countsByType := make(map[int]int)
	var (
		resolved     bool
		constantKeys []string
	)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return 0, 0, fmt.Errorf("decode netlog %q: %w", path, err)
		}
		key, isString := keyToken.(string)
		if !isString {
			return 0, 0, fmt.Errorf("decode netlog %q: unexpected top-level key %v", path, keyToken)
		}
		switch key {
		case "constants":
			var constants map[string]json.RawMessage
			if err = decoder.Decode(&constants); err != nil {
				return 0, 0, fmt.Errorf("decode netlog constants %q: %w", path, err)
			}
			id, found, keys := netLogEventID(constants, eventName)
			constantKeys = keys
			if found {
				resolvedID = id
				resolved = true
			}
		case "events":
			token, err := decoder.Token()
			if err != nil {
				return 0, 0, fmt.Errorf("decode netlog events %q: %w", path, err)
			}
			if token != json.Delim('[') {
				return 0, 0, fmt.Errorf("decode netlog %q: events is not an array", path)
			}
			for decoder.More() {
				var event struct {
					Type int `json:"type"`
				}
				if err = decoder.Decode(&event); err != nil {
					return 0, 0, fmt.Errorf("decode netlog event %q: %w", path, err)
				}
				countsByType[event.Type]++
			}
			if _, err = decoder.Token(); err != nil { // closing ']'
				return 0, 0, fmt.Errorf("decode netlog events %q: %w", path, err)
			}
		default:
			var skip json.RawMessage
			if err = decoder.Decode(&skip); err != nil {
				return 0, 0, fmt.Errorf("decode netlog %q: key %q: %w", path, key, err)
			}
		}
	}
	if _, err = decoder.Token(); err != nil { // closing '}'
		return 0, 0, fmt.Errorf("decode netlog %q: %w", path, err)
	}
	if !resolved {
		if len(constantKeys) == 0 {
			return 0, 0, fmt.Errorf("netlog %q has no event-type constants, so %q cannot be resolved",
				path, eventName)
		}
		return 0, 0, fmt.Errorf("netlog %q does not name %q; it knows: %s",
			path, eventName, strings.Join(constantKeys, ", "))
	}
	return countsByType[resolvedID], resolvedID, nil
}

// netLogEventID finds the numeric id of eventName in whichever constants sub-map names it, and
// returns the names it saw so a failure can say what the file does contain.
func netLogEventID(constants map[string]json.RawMessage, eventName string) (id int, found bool, seen []string) {
	for _, raw := range constants {
		var names map[string]int
		if err := json.Unmarshal(raw, &names); err != nil {
			continue
		}
		for name, value := range names {
			if len(seen) < netLogMaxReportedKeys {
				seen = append(seen, name)
			}
			if name == eventName {
				return value, true, seen
			}
		}
	}
	sort.Strings(seen)
	if len(seen) > netLogMaxReportedKeys {
		seen = seen[:netLogMaxReportedKeys]
	}
	return 0, false, seen
}

func writeNetLog(t *testing.T, content string) string {
	t.Helper()
	path := t.TempDir() + "/netlog.json"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestNetLogEventCountResolvesTheIDFromTheFile is the regression guard for the old hardcoded id: the
// same file with two different ids must both resolve, and the counts must follow the ids rather than
// any literal in the test.
func TestNetLogEventCountResolvesTheIDFromTheFile(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name     string
		id       int
		sessions int
	}{
		{"current chromium id", 267, 3},
		{"previous chromium id", 249, 2},
		{"some future id", 512, 4},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			events := make([]string, 0, testCase.sessions)
			for i := 0; i < testCase.sessions; i++ {
				events = append(events, fmt.Sprintf(`{"type":%d,"phase":0}`, testCase.id))
			}
			path := writeNetLog(t, fmt.Sprintf(
				`{"constants":{"logEventTypes":{"HTTP2_SESSION_INITIALIZED":%d}},`+
					`"events":[%s]}`, testCase.id, strings.Join(events, ",")))
			count, resolved, err := netLogEventCount(path, "HTTP2_SESSION_INITIALIZED")
			require.NoError(t, err)
			require.Equal(t, testCase.id, resolved)
			require.Equal(t, testCase.sessions, count)
		})
	}
}

// TestNetLogEventCountIgnoresOtherEventTypes proves the counter is not a substring search: the file
// mentions the right id inside a different event's params and in the constants block itself.
func TestNetLogEventCountIgnoresOtherEventTypes(t *testing.T) {
	t.Parallel()
	path := writeNetLog(t, `{"constants":{"logEventTypes":{"HTTP2_SESSION_INITIALIZED":267}},`+
		`"events":[{"type":5,"params":{"note":"267"}},{"type":267},{"type":267}]}`)
	count, resolved, err := netLogEventCount(path, "HTTP2_SESSION_INITIALIZED")
	require.NoError(t, err)
	require.Equal(t, 267, resolved)
	require.Equal(t, 2, count, "only the events whose own type is the id may count")
}

// TestNetLogEventCountAcceptsConstantsAfterEvents proves order independence.
func TestNetLogEventCountAcceptsConstantsAfterEvents(t *testing.T) {
	t.Parallel()
	path := writeNetLog(t, `{"events":[{"type":267},{"type":267},{"type":267}],`+
		`"constants":{"logEventTypes":{"HTTP2_SESSION_INITIALIZED":267}}}`)
	count, _, err := netLogEventCount(path, "HTTP2_SESSION_INITIALIZED")
	require.NoError(t, err)
	require.Equal(t, 3, count)
}

// TestNetLogEventCountFailsLoudly covers the shapes that must be an error, not a silent zero.
func TestNetLogEventCountFailsLoudly(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name    string
		content string
		message string
	}{
		{
			name:    "no constants",
			content: `{"events":[{"type":267}]}`,
			message: "no event-type constants",
		},
		{
			name:    "unknown event name",
			content: `{"constants":{"logEventTypes":{"SOMETHING_ELSE":7}},"events":[]}`,
			message: "does not name",
		},
		{
			name:    "not an object",
			content: `[]`,
			message: "not a JSON object",
		},
		{
			name:    "truncated",
			content: `{"constants":`,
			message: "decode netlog",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			path := writeNetLog(t, testCase.content)
			_, _, err := netLogEventCount(path, "HTTP2_SESSION_INITIALIZED")
			require.Error(t, err)
			require.Contains(t, err.Error(), testCase.message)
		})
	}
}

// TestNetLogEventCountZeroSessions proves a valid file with no sessions is a zero, not an error, so
// the self-test reports "the pool isolation did not happen" rather than "the parser broke".
func TestNetLogEventCountZeroSessions(t *testing.T) {
	t.Parallel()
	path := writeNetLog(t, `{"constants":{"logEventTypes":{"HTTP2_SESSION_INITIALIZED":267}},"events":[{"type":1}]}`)
	count, resolved, err := netLogEventCount(path, "HTTP2_SESSION_INITIALIZED")
	require.NoError(t, err)
	require.Equal(t, 267, resolved)
	require.Zero(t, count)
}
