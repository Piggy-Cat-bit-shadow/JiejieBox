package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The JSONL is the source of truth, so the readers below work off the DECODED LINES rather than
// off the Go structs.
//
// That is not laziness: a gate that read the structs would keep passing after a field was renamed
// in the writer and forgotten in the reader, because the struct would still have a zero. Reading
// the marshalled form means the gate verifies the artefact that a person or another tool will
// actually open, and a missing path is reported as missing instead of as a zero.

type recordMap map[string]any

// decodeLines parses JSONL text. Numbers are kept as json.Number so that a byte count never goes
// through float64 and comes back 1 apart from what was written.
func decodeLines(lines []string) ([]recordMap, error) {
	records := make([]recordMap, 0, len(lines))
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
		decoder.UseNumber()
		var record recordMap
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("line %d is not a JSON object: %w", index+1, err)
		}
		records = append(records, record)
	}
	return records, nil
}

// resolve walks a dotted path through nested objects.
func (m recordMap) resolve(path string) (any, bool) {
	var current any = map[string]any(m)
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// numeric converts a resolved value to a number for comparison. Booleans become 1 and 0 so that a
// business check like "the traffic was verified" can be a budget rule; null and anything else are
// NOT numbers, which is how a missing reading stays missing.
func numeric(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case bool:
		if typed {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

// specField reads one field of the record's spec block as a string.
func (m recordMap) specField(key string) string {
	spec, ok := m["spec"].(map[string]any)
	if !ok {
		return ""
	}
	value, ok := spec[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

// specActivity reads the activity history the run measured, in the canonical form the budget's
// selector compares against.
//
// The spec echoes the list as the caller gave it ("probe,traffic"), while a budget rule names a set
// ("traffic,probe"). A comparison on the raw string would make the same measurement match or not
// depending on the order the command line happened to use, so both sides go through activityKey.
func (m recordMap) specActivity() string {
	spec, ok := m["spec"].(map[string]any)
	if !ok {
		return ""
	}
	switch typed := spec["activity"].(type) {
	case []any:
		list := make([]string, 0, len(typed))
		for _, entry := range typed {
			if text, isText := entry.(string); isText {
				list = append(list, text)
			}
		}
		return activityKey(list)
	case []string:
		return activityKey(typed)
	case string:
		return activityKey(strings.Split(typed, ","))
	default:
		return ""
	}
}

// activityKey is the canonical form of an activity list: sorted, de-duplicated, comma-joined. The
// list is a SET of activities, not a sequence, and two runs that performed the same activities are
// the same population whatever order they were requested in.
func activityKey(list []string) string {
	if len(list) == 0 {
		return ""
	}
	seen := make(map[string]bool, len(list))
	unique := make([]string, 0, len(list))
	for _, entry := range list {
		entry = strings.TrimSpace(entry)
		if entry == "" || seen[entry] {
			continue
		}
		seen[entry] = true
		unique = append(unique, entry)
	}
	sort.Strings(unique)
	return strings.Join(unique, ",")
}

func (m recordMap) stage() string {
	value, _ := m["stage"].(string)
	return value
}

func (m recordMap) status() string {
	value, _ := m["status"].(string)
	return value
}

func (m recordMap) runID() string {
	value, _ := m["run_id"].(string)
	return value
}

func (m recordMap) recordKind() string {
	value, _ := m["record"].(string)
	return value
}

// runStages returns the stage names of one run, in order.
func runStages(records []recordMap, runID string) []string {
	var stages []string
	for _, record := range records {
		if record.runID() == runID && record.recordKind() == recordStage {
			stages = append(stages, record.stage())
		}
	}
	return stages
}

// runIDs returns the distinct run ids in file order.
func runIDs(records []recordMap) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, record := range records {
		id := record.runID()
		if id == "" || seen[id] {
			continue
		}
		// A child_result line names the child it summarises; its own run id is the child's, so the
		// id is the natural key either way.
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func intFromRecord(record recordMap, path string) (int, bool) {
	value, ok := record.resolve(path)
	if !ok {
		return 0, false
	}
	number, ok := numeric(value)
	if !ok {
		return 0, false
	}
	return int(number), true
}

func floatString(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
