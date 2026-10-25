package sessionview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestProjectReferenceKeys(t *testing.T) {
	shape := &Shape{Children: map[string]*Shape{"tasks": {ReferenceKeys: true}}}
	p := Projector{Shape: shape, Platform: "test", Lookup: lookup}
	raw := []byte(`{"tasks":{"first":1,"second":[{"id":"shell","sessionID":"opaque"}],"ocm_existing":3,"":0},"opaque":{"first":4}}`)
	result, err := p.JSON(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	tasks := got["tasks"].(map[string]any)
	if len(tasks) != 4 || tasks["ocm_test_first"] != float64(1) || tasks["ocm_existing"] != float64(3) || tasks[""] != float64(0) {
		t.Fatalf("dictionary keys changed incorrectly: %s", result)
	}
	if _, ok := tasks["ocm_test_second"]; !ok || !strings.Contains(string(result), `"sessionID":"opaque"`) || !strings.Contains(string(result), `"opaque":{"first":4}`) {
		t.Fatalf("values or undeclared keys changed: %s", result)
	}
}

func TestReferenceKeysFailClosed(t *testing.T) {
	p := Projector{Shape: &Shape{ReferenceKeys: true}, Lookup: func(context.Context, string, []string) (map[string]string, error) {
		return nil, errors.New("owner unavailable")
	}}
	if _, err := p.JSON(t.Context(), []byte(`{"native":1}`)); err == nil {
		t.Fatal("unresolved session key was exposed")
	}
	p.RequireCanonical = true
	if _, err := p.JSON(t.Context(), []byte(`{"native":1}`)); err == nil {
		t.Fatal("native dictionary key passed canonical validation")
	}
	raw := []byte(`{"ocm_e9d4e552-5a10-44ef-bbd3-3b6d9f94ed4a":1}`)
	if result, err := p.JSON(t.Context(), raw); err != nil || string(result) != string(raw) {
		t.Fatalf("canonical dictionary rejected: %s %v", result, err)
	}
}
