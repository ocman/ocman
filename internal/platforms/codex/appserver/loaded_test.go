package codexappserver_test

import (
	"encoding/json"
	"slices"
	"testing"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

func TestListLoadedThreadsPaginates(t *testing.T) {
	c, s := attach(t, codexappserver.Options{})
	s.Handle(codexappserver.MethodThreadLoadedList, func(raw json.RawMessage) (any, error) {
		var p struct{ Cursor string }
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Error(err)
		}
		if p.Cursor == "" {
			return map[string]any{"data": []string{"one"}, "nextCursor": "next"}, nil
		}
		if p.Cursor != "next" {
			t.Errorf("cursor = %q", p.Cursor)
		}
		return map[string]any{"data": []string{"two"}}, nil
	})
	got, err := c.ListLoadedThreads(t.Context())
	if err != nil || !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("loaded threads = %v, %v", got, err)
	}
}
