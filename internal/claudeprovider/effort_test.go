package claudeprovider

import (
	"slices"
	"testing"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestDirectoryCatalogReasoningEffort(t *testing.T) {
	response := `{"subtype":"success","request_id":"ocman-initialize","response":{"models":[` +
		`{"value":"opus","resolvedModel":"claude-opus-4-7","displayName":"Claude Opus 4.7","supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"]},` +
		`{"value":"sonnet","resolvedModel":"sonnet","displayName":"Claude Sonnet","supportsEffort":true,"supportedEffortLevels":["low","medium","high"]},` +
		`{"value":"haiku","displayName":"Claude Haiku","supportsEffort":false}]}}`
	h := serveLive(t, initFixture(t, response), nil)
	for range 2 { // The cached catalog must keep the same effort options.
		cat, err := h.adapter.DirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: h.dir})
		if err != nil {
			t.Fatal(err)
		}
		for _, model := range []string{"opus", "claude-opus-4-7", "sonnet", "haiku"} {
			i := slices.IndexFunc(cat.Models.Models, func(m platforms.SessionModel) bool { return m.Model == model })
			if i < 0 {
				t.Fatalf("catalog misses %s: %+v", model, cat.Models.Models)
			}
			entry := cat.Models.Models[i]
			want := []string{"low", "medium", "high", "xhigh", "max"}
			switch model {
			case "sonnet":
				want = want[:3]
			case "haiku":
				want = nil
			}
			if !slices.Equal(entry.Reasoning, want) {
				t.Fatalf("%s reasoning = %v, want %v", model, entry.Reasoning, want)
			}
		}
	}
}

func TestReasoningEffortReachesClaude(t *testing.T) {
	h := startLive(t, claudetest.Fixture("plain.jsonl"), nil)
	id := h.create(t)
	for i, effort := range []string{"low", "high", ""} {
		req := platforms.SendMessageRequest{SessionID: id, Message: "hi", Model: "anthropic/opus", Reasoning: effort}
		var err error
		if i == 1 {
			err = h.adapter.ExecuteCommand(t.Context(), platforms.ExecuteCommandRequest{SessionID: id, Command: "review", Model: req.Model, Reasoning: effort})
		} else {
			err = h.adapter.SendMessage(t.Context(), req)
		}
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "turn idle", func() bool { _, _, n := h.events.get(id); return n == i+1 })
		args := h.launchArgs(t)
		j := slices.Index(args, "--effort")
		if effort == "" {
			if j >= 0 {
				t.Fatalf("default effort should omit --effort: %q", args)
			}
		} else if j < 0 || j+1 >= len(args) || args[j+1] != effort {
			t.Fatalf("effort %s missing from launch args: %q", effort, args)
		}
	}
}
