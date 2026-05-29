package opencode

import (
	"testing"
)

// --- truncatePartOutput tests ---

func TestTruncatePartOutput_ShortText(t *testing.T) {
	part := map[string]interface{}{
		"text": "short text",
	}
	truncatePartOutput(part)
	if part["text"] != "short text" {
		t.Errorf("short text should not be truncated")
	}
}

func TestTruncatePartOutput_LongText(t *testing.T) {
	long := make([]byte, maxOutputLen+100)
	for i := range long {
		long[i] = 'x'
	}
	part := map[string]interface{}{
		"text": string(long),
	}
	truncatePartOutput(part)
	text := part["text"].(string)
	if len(text) <= maxOutputLen {
		t.Errorf("expected truncated text > maxOutputLen marker, got len=%d", len(text))
	}
	// The truncated text should be maxOutputLen + the suffix
	expected := maxOutputLen + len("\n... (truncated)")
	if len(text) != expected {
		t.Errorf("expected len=%d, got %d", expected, len(text))
	}
}

func TestTruncatePartOutput_StateOutput(t *testing.T) {
	long := make([]byte, maxOutputLen+50)
	for i := range long {
		long[i] = 'y'
	}
	part := map[string]interface{}{
		"state": map[string]interface{}{
			"output": string(long),
		},
	}
	truncatePartOutput(part)
	state := part["state"].(map[string]interface{})
	output := state["output"].(string)
	if len(output) > maxOutputLen+50 {
		t.Errorf("state.output should be truncated, got len=%d", len(output))
	}
}

func TestTruncatePartOutput_MetadataOutput(t *testing.T) {
	long := make([]byte, maxOutputLen+50)
	for i := range long {
		long[i] = 'z'
	}
	part := map[string]interface{}{
		"state": map[string]interface{}{
			"metadata": map[string]interface{}{
				"output": string(long),
			},
		},
	}
	truncatePartOutput(part)
	state := part["state"].(map[string]interface{})
	meta := state["metadata"].(map[string]interface{})
	output := meta["output"].(string)
	if len(output) > maxOutputLen+50 {
		t.Errorf("state.metadata.output should be truncated, got len=%d", len(output))
	}
}

func TestTruncatePartOutput_NoTextOrState(t *testing.T) {
	part := map[string]interface{}{
		"type": "tool-call",
		"id":   "abc",
	}
	// Should not panic
	truncatePartOutput(part)
}

// --- convertOpenCodeMessages tests ---

func TestConvertOpenCodeMessages_Empty(t *testing.T) {
	messages, parts := convertOpenCodeMessages(nil)
	if len(messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(messages))
	}
	if len(parts) != 0 {
		t.Errorf("expected 0 parts, got %d", len(parts))
	}
}

func TestConvertOpenCodeMessages_SkipsNilInfo(t *testing.T) {
	input := []map[string]interface{}{
		{"noinfo": true},
	}
	messages, _ := convertOpenCodeMessages(input)
	if len(messages) != 0 {
		t.Errorf("expected 0 messages for nil info, got %d", len(messages))
	}
}

func TestConvertOpenCodeMessages_ExtractsFields(t *testing.T) {
	input := []map[string]interface{}{
		{
			"info": map[string]interface{}{
				"id":        "msg-1",
				"sessionID": "sess-1",
				"role":      "user",
				"time": map[string]interface{}{
					"created": float64(1000),
				},
			},
			"parts": []interface{}{
				map[string]interface{}{
					"id":        "part-1",
					"messageID": "msg-1",
					"sessionID": "sess-1",
					"type":      "text",
					"text":      "hello",
				},
			},
		},
	}
	messages, parts := convertOpenCodeMessages(input)
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0]["id"] != "msg-1" {
		t.Errorf("expected message id 'msg-1', got %v", messages[0]["id"])
	}
	if messages[0]["timeCreated"] != int64(1000) {
		t.Errorf("expected timeCreated=1000, got %v", messages[0]["timeCreated"])
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0]["id"] != "part-1" {
		t.Errorf("expected part id 'part-1', got %v", parts[0]["id"])
	}
}

func TestConvertOpenCodeMessages_SkipsFilteredPartTypes(t *testing.T) {
	input := []map[string]interface{}{
		{
			"info": map[string]interface{}{
				"id":   "msg-1",
				"role": "assistant",
			},
			"parts": []interface{}{
				map[string]interface{}{"type": "step-start", "id": "p1"},
				map[string]interface{}{"type": "step-finish", "id": "p2"},
				map[string]interface{}{"type": "snapshot", "id": "p3"},
				map[string]interface{}{"type": "text", "id": "p4", "text": "hello"},
			},
		},
	}
	_, parts := convertOpenCodeMessages(input)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part (filtered types skipped), got %d", len(parts))
	}
}

func TestConvertOpenCodeMessages_RemovesSummaryAndPath(t *testing.T) {
	input := []map[string]interface{}{
		{
			"info": map[string]interface{}{
				"id":      "msg-1",
				"role":    "assistant",
				"summary": "should be removed",
				"path":    "/some/path",
			},
		},
	}
	messages, _ := convertOpenCodeMessages(input)
	info := messages[0]["data"].(map[string]interface{})
	if _, ok := info["summary"]; ok {
		t.Error("summary field should have been deleted")
	}
	if _, ok := info["path"]; ok {
		t.Error("path field should have been deleted")
	}
}

// --- isSynthesizedTerminal tests ---

func TestIsSynthesizedTerminal_ShellOnlyMessage(t *testing.T) {
	// Mirrors the synthesized envelope produced by POST /session/{id}/shell:
	// a single completed bash tool part, no step-start.
	raw := map[string]interface{}{
		"info": map[string]interface{}{"id": "msg-1", "role": "assistant"},
		"parts": []interface{}{
			map[string]interface{}{
				"type": "tool",
				"tool": "bash",
				"state": map[string]interface{}{"status": "completed"},
			},
		},
	}
	if !isSynthesizedTerminal(raw) {
		t.Error("expected shell-only assistant envelope to be classified as synthesized terminal")
	}
}

func TestIsSynthesizedTerminal_LLMTurnWithStepStart(t *testing.T) {
	raw := map[string]interface{}{
		"info": map[string]interface{}{"id": "msg-1", "role": "assistant"},
		"parts": []interface{}{
			map[string]interface{}{"type": "step-start"},
			map[string]interface{}{"type": "text", "text": "hello"},
		},
	}
	if isSynthesizedTerminal(raw) {
		t.Error("LLM turn with step-start must not be classified as synthesized terminal")
	}
}

func TestIsSynthesizedTerminal_RunningToolMidFlight(t *testing.T) {
	raw := map[string]interface{}{
		"info": map[string]interface{}{"id": "msg-1", "role": "assistant"},
		"parts": []interface{}{
			map[string]interface{}{
				"type":  "tool",
				"tool":  "bash",
				"state": map[string]interface{}{"status": "running"},
			},
		},
	}
	if isSynthesizedTerminal(raw) {
		t.Error("running tool must not be classified as synthesized terminal")
	}
}

func TestIsSynthesizedTerminal_NoParts(t *testing.T) {
	raw := map[string]interface{}{
		"info":  map[string]interface{}{"id": "msg-1", "role": "assistant"},
		"parts": []interface{}{},
	}
	if isSynthesizedTerminal(raw) {
		t.Error("empty parts must not be classified as synthesized terminal (still genuinely busy)")
	}
}

func TestIsSynthesizedTerminal_NilParts(t *testing.T) {
	raw := map[string]interface{}{
		"info": map[string]interface{}{"id": "msg-1", "role": "assistant"},
	}
	if isSynthesizedTerminal(raw) {
		t.Error("missing parts key must not be classified as synthesized terminal")
	}
}

// --- computeMessageStats tests ---

func TestComputeMessageStats_Empty(t *testing.T) {
	stats := computeMessageStats(nil)
	if stats.totalInputTokens != 0 || stats.totalOutputTokens != 0 || stats.totalCost != 0 {
		t.Errorf("expected zero stats for empty input")
	}
}

func TestComputeMessageStats_Aggregation(t *testing.T) {
	messages := []map[string]interface{}{
		{
			"timeCreated": int64(1000),
			"data": map[string]interface{}{
				"role": "user",
			},
		},
		{
			"timeCreated": int64(2000),
			"data": map[string]interface{}{
				"role": "assistant",
				"tokens": map[string]interface{}{
					"input":     float64(100),
					"output":    float64(50),
					"reasoning": float64(10),
					"cache": map[string]interface{}{
						"read":  float64(5),
						"write": float64(3),
					},
				},
				"cost": float64(0.01),
			},
		},
	}
	stats := computeMessageStats(messages)
	if stats.totalInputTokens != 100 {
		t.Errorf("totalInputTokens = %v, want 100", stats.totalInputTokens)
	}
	if stats.totalOutputTokens != 50 {
		t.Errorf("totalOutputTokens = %v, want 50", stats.totalOutputTokens)
	}
	if stats.totalCost != 0.01 {
		t.Errorf("totalCost = %v, want 0.01", stats.totalCost)
	}
	if stats.durationMs != 1000 {
		t.Errorf("durationMs = %d, want 1000", stats.durationMs)
	}
	// contextTokenCount should be the sum of all token fields for the last assistant message with output > 0
	expectedCtx := float64(100 + 50 + 10 + 5 + 3)
	if stats.contextTokenCount != expectedCtx {
		t.Errorf("contextTokenCount = %v, want %v", stats.contextTokenCount, expectedCtx)
	}
}

// TestComputeMessageStats_ActiveDuration verifies that activeDurationMs sums
// (time.completed - time.created) across assistant messages only, excluding
// idle gaps between turns (user think time / permission waits between
// assistant turns).
func TestComputeMessageStats_ActiveDuration(t *testing.T) {
	// Timeline (timeCreated of each message):
	//   t=1000: user message
	//   t=2000..3500: assistant turn 1 (1500 ms active)
	//   t=3500..7000: user reads + responds at t=7000 (idle gap, excluded)
	//   t=7000: user message
	//   t=7100..9100: assistant turn 2 (2000 ms active)
	// Wall-clock duration (last timeCreated - first timeCreated)
	//                             = 7100 - 1000 = 6100 ms
	// Active duration             = 1500 + 2000 = 3500 ms
	messages := []map[string]interface{}{
		{
			"timeCreated": int64(1000),
			"data": map[string]interface{}{
				"role": "user",
			},
		},
		{
			"timeCreated": int64(2000),
			"data": map[string]interface{}{
				"role": "assistant",
				"time": map[string]interface{}{
					"created":   float64(2000),
					"completed": float64(3500),
				},
			},
		},
		{
			"timeCreated": int64(7000),
			"data": map[string]interface{}{
				"role": "user",
			},
		},
		{
			"timeCreated": int64(7100),
			"data": map[string]interface{}{
				"role": "assistant",
				"time": map[string]interface{}{
					"created":   float64(7100),
					"completed": float64(9100),
				},
			},
		},
	}
	stats := computeMessageStats(messages)
	if stats.durationMs != 6100 {
		t.Errorf("durationMs = %d, want 6100 (wall-clock)", stats.durationMs)
	}
	if stats.activeDurationMs != 3500 {
		t.Errorf("activeDurationMs = %d, want 3500 (sum of assistant turn durations)", stats.activeDurationMs)
	}
}

// TestComputeMessageStats_ActiveDuration_InFlightAssistant verifies that an
// assistant message still in flight (no time.completed) does not contribute
// to activeDurationMs. We can't know how long it has actually run vs. been
// waiting, so we skip it rather than guess.
func TestComputeMessageStats_ActiveDuration_InFlightAssistant(t *testing.T) {
	messages := []map[string]interface{}{
		{
			"timeCreated": int64(1000),
			"data": map[string]interface{}{
				"role": "assistant",
				"time": map[string]interface{}{
					"created":   float64(1000),
					"completed": float64(2500), // completed turn: 1500 ms
				},
			},
		},
		{
			"timeCreated": int64(3000),
			"data": map[string]interface{}{
				"role": "assistant",
				"time": map[string]interface{}{
					"created": float64(3000),
					// no completed -> in flight, skip
				},
			},
		},
	}
	stats := computeMessageStats(messages)
	if stats.activeDurationMs != 1500 {
		t.Errorf("activeDurationMs = %d, want 1500 (in-flight message skipped)", stats.activeDurationMs)
	}
}

// TestComputeMessageStats_ActiveDuration_IgnoresUserAndBogusTimes ensures user
// messages are never counted (they have no LLM duration) and that malformed
// time data (completed <= created) is skipped rather than producing a
// negative contribution.
func TestComputeMessageStats_ActiveDuration_IgnoresUserAndBogusTimes(t *testing.T) {
	messages := []map[string]interface{}{
		{
			"timeCreated": int64(1000),
			"data": map[string]interface{}{
				"role": "user",
				// A user message with a (nonsensical) time block must not contribute.
				"time": map[string]interface{}{
					"created":   float64(1000),
					"completed": float64(9999999),
				},
			},
		},
		{
			"timeCreated": int64(2000),
			"data": map[string]interface{}{
				"role": "assistant",
				"time": map[string]interface{}{
					"created":   float64(2000),
					"completed": float64(1000), // bogus: completed before created
				},
			},
		},
		{
			"timeCreated": int64(3000),
			"data": map[string]interface{}{
				"role": "assistant",
				"time": map[string]interface{}{
					"created":   float64(3000),
					"completed": float64(3700),
				},
			},
		},
	}
	stats := computeMessageStats(messages)
	if stats.activeDurationMs != 700 {
		t.Errorf("activeDurationMs = %d, want 700 (user + bogus skipped)", stats.activeDurationMs)
	}
}

// --- paginateUntyped tests ---

func TestPaginateUntyped(t *testing.T) {
	msgs := []map[string]interface{}{
		{"id": "m1"}, {"id": "m2"}, {"id": "m3"}, {"id": "m4"}, {"id": "m5"},
	}

	tests := []struct {
		name    string
		limit   int
		offset  int
		wantLen int
		wantIDs []string
	}{
		{"last 2", 2, 0, 2, []string{"m4", "m5"}},
		{"last 2 offset 1", 2, 1, 2, []string{"m3", "m4"}},
		{"all", 10, 0, 5, []string{"m1", "m2", "m3", "m4", "m5"}},
		{"offset beyond", 2, 10, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ids := paginateUntyped(msgs, tt.limit, tt.offset)
			if len(result) != tt.wantLen {
				t.Fatalf("got %d results, want %d", len(result), tt.wantLen)
			}
			for i, wantID := range tt.wantIDs {
				if result[i]["id"] != wantID {
					t.Errorf("result[%d][id] = %v, want %q", i, result[i]["id"], wantID)
				}
				if !ids[wantID] {
					t.Errorf("ids should contain %q", wantID)
				}
			}
		})
	}
}

func TestPaginateUntyped_Empty(t *testing.T) {
	result, ids := paginateUntyped(nil, 10, 0)
	if result != nil {
		t.Errorf("expected nil result for empty input")
	}
	if ids != nil {
		t.Errorf("expected nil ids for empty input")
	}
}

// --- filterPartsUntyped tests ---

func TestFilterPartsUntyped(t *testing.T) {
	parts := []map[string]interface{}{
		{"messageId": "m1", "id": "p1"},
		{"messageId": "m2", "id": "p2"},
		{"messageId": "m1", "id": "p3"},
	}
	ids := map[string]bool{"m1": true}
	result := filterPartsUntyped(parts, ids)
	if len(result) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(result))
	}
}

func TestFilterPartsUntyped_NilIDs(t *testing.T) {
	parts := []map[string]interface{}{{"messageId": "m1"}}
	result := filterPartsUntyped(parts, nil)
	if result != nil {
		t.Errorf("expected nil for nil ids")
	}
}

// --- copyMap tests ---

func TestCopyMap(t *testing.T) {
	original := map[string]string{"a": "1", "b": "2"}
	cp := copyMap(original)

	if len(cp) != 2 || cp["a"] != "1" || cp["b"] != "2" {
		t.Errorf("copy doesn't match original")
	}

	// Mutating the copy should not affect the original
	cp["c"] = "3"
	if _, ok := original["c"]; ok {
		t.Error("mutating copy affected original")
	}
}

func TestCopyMap_Nil(t *testing.T) {
	cp := copyMap(nil)
	if cp == nil {
		t.Error("expected non-nil empty map")
	}
	if len(cp) != 0 {
		t.Error("expected empty map")
	}
}
