package statev2

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestStatusSummaryMatchesFullSummaryInferenceInputs(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "second")
	for _, tc := range []struct {
		name, role, finish, errorName, ext string
		hasError, hidden, deleted          bool
	}{
		{name: "empty", ext: `{}`},
		{name: "user", role: "user", ext: `{}`},
		{name: "waiting", role: "assistant", finish: "stop", ext: `{}`},
		{name: "unfinished", role: "assistant", ext: `{}`},
		{name: "tool", role: "assistant", finish: "tool-calls", ext: `{}`},
		{name: "unknown", role: "assistant", finish: "unknown", ext: `{}`},
		{name: "error", role: "assistant", hasError: true, ext: `{}`},
		{name: "named error", role: "assistant", errorName: "APIError", hasError: true, ext: `{}`},
		{name: "name without error", role: "assistant", errorName: "APIError", ext: `{}`},
		{name: "hidden", role: "assistant", hasError: true, hidden: true, ext: `{}`},
		{name: "deleted", role: "assistant", finish: "stop", deleted: true, ext: `{"InferredStatus":"done"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := summaryBatch("first", tc.name, "")
			b.Session.Extensions = json.RawMessage(tc.ext)
			b.Session.SourceDeleted = tc.deleted
			b.Messages, b.Parts = nil, nil
			if tc.role != "" {
				b.Messages = []Message{{ID: "latest", Role: tc.role, Finish: tc.finish, HasError: tc.hasError, ErrorName: tc.errorName, ConversationVisible: !tc.hidden}}
			}
			commit(t, d, b)
			// Same ID in another physical source must not supply status inputs.
			other := summaryBatch("second", tc.name, "")
			commit(t, d, other)
			full, err := d.SessionSummaries(t.Context(), "first", "", tc.name)
			if err != nil || len(full) != 1 {
				t.Fatalf("full: %+v %v", full, err)
			}
			narrow, err := d.SessionStatusSummary(t.Context(), b.Session.Key)
			if err != nil {
				t.Fatal(err)
			}
			want := full[0]
			if narrow.Totals.Role != want.Totals.Role || narrow.Totals.Finish != want.Totals.Finish || narrow.Totals.Error != want.Totals.Error || string(narrow.Extensions) != string(want.Extensions) || narrow.Directory != want.Directory {
				t.Fatalf("narrow %+v differs from full %+v", narrow, want)
			}
		})
	}
	if _, err := d.SessionStatusSummary(t.Context(), Key{"first", "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	_ = d.Close()
	if _, err := d.SessionStatusSummary(t.Context(), Key{"first", "empty"}); err == nil {
		t.Fatal("closed store accepted")
	}
}
