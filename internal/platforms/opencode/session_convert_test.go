package opencode

import (
	"encoding/json"
	"testing"
)

func TestSessionFromOpenCode_PopulatesPlatformAndLiveConnection(t *testing.T) {
	oc := map[string]interface{}{
		"id":        "s1",
		"projectID": "p1",
		"title":     "hello",
		"directory": "/tmp/proj",
		"time": map[string]interface{}{
			"created": float64(1_000),
			"updated": float64(2_000),
		},
	}
	s := sessionFromOpenCode(oc, messageStats{}, 0, "done")
	if s.Platform != "opencode" {
		t.Errorf("expected Platform=opencode, got %q", s.Platform)
	}
	if !s.LiveConnection {
		t.Error("expected LiveConnection=true for live-path sessions")
	}
	if s.ID != "s1" || s.ProjectID != "p1" || s.Title != "hello" || s.Directory != "/tmp/proj" {
		t.Errorf("unexpected session scalars: %+v", s)
	}
	if s.TimeCreated != 1000 || s.TimeUpdated != 2000 {
		t.Errorf("expected times 1000/2000, got %d/%d", s.TimeCreated, s.TimeUpdated)
	}
}

func TestSessionFromOpenCode_MissingFieldsStayZero(t *testing.T) {
	s := sessionFromOpenCode(map[string]interface{}{}, messageStats{}, 0, "done")
	if s.ID != "" || s.Title != "" || s.TimeCreated != 0 {
		t.Errorf("expected zeros for missing fields, got %+v", s)
	}
	if s.SummaryAdditions != nil || s.SummaryFiles != nil || s.ShareURL != nil {
		t.Errorf("expected nil pointers for missing optional fields, got %+v", s)
	}
}

func TestSessionFromOpenCode_OptionalSummaryPointers(t *testing.T) {
	oc := map[string]interface{}{
		"id": "s1",
		"summary": map[string]interface{}{
			"additions": float64(5),
			"files":     float64(2),
		},
	}
	s := sessionFromOpenCode(oc, messageStats{}, 0, "done")
	if s.SummaryAdditions == nil || *s.SummaryAdditions != 5 {
		t.Errorf("expected SummaryAdditions=5, got %+v", s.SummaryAdditions)
	}
	if s.SummaryFiles == nil || *s.SummaryFiles != 2 {
		t.Errorf("expected SummaryFiles=2, got %+v", s.SummaryFiles)
	}
	if s.SummaryDeletions != nil {
		t.Errorf("expected SummaryDeletions=nil, got %+v", s.SummaryDeletions)
	}
}

func TestTypedMessagesFromUntyped_RoundTripsData(t *testing.T) {
	untyped := []map[string]interface{}{
		{
			"id":          "m1",
			"sessionId":   "s1",
			"timeCreated": float64(1234),
			"data":        map[string]interface{}{"role": "user"},
		},
	}
	out := typedMessagesFromUntyped(untyped)
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	m := out[0]
	if m.ID != "m1" || m.SessionID != "s1" || m.TimeCreated != 1234 {
		t.Errorf("unexpected scalar fields: %+v", m)
	}
	// Verify .data round-trips.
	var parsed map[string]interface{}
	if err := json.Unmarshal(m.Data, &parsed); err != nil {
		t.Fatalf("data didn't round-trip: %v", err)
	}
	if parsed["role"] != "user" {
		t.Errorf("expected data.role=user, got %+v", parsed)
	}
}

func TestTypedMessagesFromUntyped_EmptyInput(t *testing.T) {
	if got := typedMessagesFromUntyped(nil); got != nil {
		t.Errorf("expected nil for nil input, got %+v", got)
	}
	if got := typedMessagesFromUntyped([]map[string]interface{}{}); got != nil {
		t.Errorf("expected nil for empty input, got %+v", got)
	}
}

func TestTypedPartsFromUntyped_RoundTripsData(t *testing.T) {
	untyped := []map[string]interface{}{
		{
			"id":        "p1",
			"messageId": "m1",
			"sessionId": "s1",
			"data":      map[string]interface{}{"type": "text", "text": "hello"},
		},
	}
	out := typedPartsFromUntyped(untyped)
	if len(out) != 1 {
		t.Fatalf("expected 1 part, got %d", len(out))
	}
	p := out[0]
	if p.ID != "p1" || p.MessageID != "m1" || p.SessionID != "s1" {
		t.Errorf("unexpected scalar fields: %+v", p)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(p.Data, &parsed); err != nil {
		t.Fatalf("data didn't round-trip: %v", err)
	}
	if parsed["type"] != "text" || parsed["text"] != "hello" {
		t.Errorf("expected parsed data text=hello, got %+v", parsed)
	}
}
