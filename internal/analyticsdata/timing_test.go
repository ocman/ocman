package analyticsdata

import "testing"

func TestSharedTimingRules(t *testing.T) {
	for _, tc := range []struct {
		name            string
		start, duration int64
		stop            string
		tools           []ToolTiming
		want            Timing
	}{
		{"plain", 1000, 10000, "stop", nil, Timing{AgentMs: 10000, TokensPerSecond: 2}},
		{"overlap", 1000, 10000, "stop", []ToolTiming{{Start: 3000, End: 9000}, {Start: 5000, End: 11000}}, Timing{AgentMs: 2000, ToolMs: 8000, TokensPerSecond: 10}},
		{"missing tools", 1000, 10000, "tool-calls", nil, Timing{UnknownMs: 10000}},
		{"unfinished tool", 1000, 10000, "stop", []ToolTiming{{Start: 2000}}, Timing{UnknownMs: 10000}},
		{"reversed tool", 1000, 10000, "stop", []ToolTiming{{Start: 5000, End: 2000}}, Timing{UnknownMs: 10000}},
		{"no start", 0, 10000, "stop", nil, Timing{UnknownMs: 10000}},
		{"no duration", 1000, 0, "stop", nil, Timing{}},
		{"short sample", 1000, 100, "stop", nil, Timing{AgentMs: 100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SummarizeTiming(tc.start, tc.duration, 20, tc.stop, tc.tools); got != tc.want {
				t.Fatalf("%+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSharedModelIdentity(t *testing.T) {
	for _, tc := range []struct {
		md                   MessageData
		provider, model, key string
	}{
		{MessageData{}, "", "", ""},
		{MessageData{ModelID: "m"}, "", "m", "m"},
		{MessageData{ProviderID: "p", ModelID: "m"}, "p", "m", "p/m"},
		{MessageData{ModelID: "ignored", Model: &ModelRef{ProviderID: "nested", ModelID: "m"}}, "nested", "m", "nested/m"},
		{MessageData{ProviderID: "p", Model: &ModelRef{ProviderID: "nested", ModelID: "ignored"}}, "p", "", ""},
	} {
		provider, model := ModelProvider(tc.md)
		if provider != tc.provider || model != tc.model || ModelKey(provider, model) != tc.key {
			t.Fatal("model identity changed", tc.md, provider, model)
		}
	}
}
