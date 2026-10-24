package opencode

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// TestBuildSessionInfo exercises buildSessionInfo across the
// happy-path combinations the OpenCode adapter encounters in the
// wild. It uses the raw JSON shapes returned by /mcp, /lsp, and
// /provider so any drift in the OpenCode wire format shows up here
// rather than at runtime.
func TestBuildSessionInfo(t *testing.T) {
	type args struct {
		sessionID    string
		tokens       int64
		cost         float64
		estCost      float64
		modelRef     string // "provider/model"
		mcpJSON      string // raw body from /mcp
		lspJSON      string // raw body from /lsp
		providerJSON string // raw body from /provider
	}
	tests := []struct {
		name string
		in   args
		want *platforms.SessionInfo
	}{
		{
			name: "mcp+lsp+model_limit",
			in: args{
				sessionID: "ses_a",
				tokens:    153_309,
				cost:      0.42,
				modelRef:  "anthropic/claude-sonnet-4",
				mcpJSON:   `{"weave":{"status":"failed","error":"Failed to get tools"},"devtoys":{"status":"needs_auth"}}`,
				lspJSON:   `[{"id":"gopls","name":"gopls","root":"","status":"connected"}]`,
				providerJSON: `{
					"all":[{"id":"anthropic","models":{"claude-sonnet-4":{"id":"claude-sonnet-4","limit":{"context":200000,"output":64000}}}}],
					"connected":["anthropic"],
					"default":{}
				}`,
			},
			want: &platforms.SessionInfo{
				SessionID: "ses_a",
				Supported: true,
				Context: platforms.ContextInfo{
					Tokens: 153_309,
					Limit:  200_000,
					Cost:   0.42,
					Model:  "anthropic/claude-sonnet-4",
				},
				MCPServers: []platforms.MCPServer{
					{Name: "devtoys", Status: "needs_auth", AuthHint: "opencode mcp auth devtoys"},
					{Name: "weave", Status: "failed", Error: "Failed to get tools"},
				},
				LSPServers: []platforms.LSPServer{
					{ID: "gopls", Name: "gopls", Status: "connected"},
				},
			},
		},
		{
			name: "no_model_no_lsp",
			in: args{
				sessionID:    "ses_b",
				tokens:       0,
				cost:         0,
				modelRef:     "",
				mcpJSON:      `{}`,
				lspJSON:      `[]`,
				providerJSON: `{"all":[],"connected":[],"default":{}}`,
			},
			want: &platforms.SessionInfo{
				SessionID:  "ses_b",
				Supported:  true,
				Context:    platforms.ContextInfo{},
				MCPServers: []platforms.MCPServer{},
				LSPServers: []platforms.LSPServer{},
			},
		},
		{
			name: "model_not_in_catalog_keeps_zero_limit",
			in: args{
				sessionID:    "ses_c",
				tokens:       1000,
				cost:         0,
				modelRef:     "ghost/missing-model",
				mcpJSON:      `{}`,
				lspJSON:      `[]`,
				providerJSON: `{"all":[{"id":"anthropic","models":{"claude-sonnet-4":{"id":"claude-sonnet-4","limit":{"context":200000}}}}],"connected":[],"default":{}}`,
			},
			want: &platforms.SessionInfo{
				SessionID: "ses_c",
				Supported: true,
				Context: platforms.ContextInfo{
					Tokens: 1000,
					Limit:  0,
					Cost:   0,
					Model:  "ghost/missing-model",
				},
				MCPServers: []platforms.MCPServer{},
				LSPServers: []platforms.LSPServer{},
			},
		},
		{
			name: "mcp_connected_status_passed_through",
			in: args{
				sessionID:    "ses_d",
				modelRef:     "",
				mcpJSON:      `{"linear":{"status":"connected"}}`,
				lspJSON:      `[]`,
				providerJSON: `{"all":[],"connected":[],"default":{}}`,
			},
			want: &platforms.SessionInfo{
				SessionID: "ses_d",
				Supported: true,
				MCPServers: []platforms.MCPServer{
					{Name: "linear", Status: "connected"},
				},
				LSPServers: []platforms.LSPServer{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mcp map[string]rawMCPEntry
			if err := json.Unmarshal([]byte(tt.in.mcpJSON), &mcp); err != nil {
				t.Fatalf("unmarshal mcp: %v", err)
			}
			var lsp []rawLSPEntry
			if err := json.Unmarshal([]byte(tt.in.lspJSON), &lsp); err != nil {
				t.Fatalf("unmarshal lsp: %v", err)
			}
			var prov OpenCodeProvidersResponse
			if err := json.Unmarshal([]byte(tt.in.providerJSON), &prov); err != nil {
				t.Fatalf("unmarshal providers: %v", err)
			}

			got := buildSessionInfo(tt.in.sessionID, tt.in.tokens, tt.in.cost, tt.in.estCost, tt.in.modelRef, mcp, lsp, &prov)

			// MCP servers come from a map in the wire format and have no
			// inherent order. Sort both sides by Name for a stable
			// comparison; the deterministic ordering is the production
			// behaviour we'll enforce in buildSessionInfo too (keeps the
			// rendered list stable across reloads).
			sort.Slice(got.MCPServers, func(i, j int) bool { return got.MCPServers[i].Name < got.MCPServers[j].Name })
			sort.Slice(tt.want.MCPServers, func(i, j int) bool { return tt.want.MCPServers[i].Name < tt.want.MCPServers[j].Name })

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildSessionInfo mismatch\n got=%+v\nwant=%+v", got, tt.want)
			}
		})
	}
}

// TestBuildSessionInfo_NilProvider verifies the function tolerates a
// nil provider catalog (live OpenCode unreachable, no fallback). The
// limit field stays at zero and the function does not panic.
func TestBuildSessionInfo_NilProvider(t *testing.T) {
	got := buildSessionInfo("ses_x", 100, 0, 0, "anthropic/claude-sonnet-4", nil, nil, nil)
	if got.Context.Limit != 0 {
		t.Errorf("Context.Limit = %d, want 0 with nil provider", got.Context.Limit)
	}
	if got.Context.Tokens != 100 {
		t.Errorf("Context.Tokens = %d, want 100", got.Context.Tokens)
	}
	if got.MCPServers == nil || got.LSPServers == nil {
		t.Errorf("MCP/LSP slices should be non-nil, got mcp=%v lsp=%v", got.MCPServers, got.LSPServers)
	}
	if got.Context.Model != "anthropic/claude-sonnet-4" {
		t.Errorf("Context.Model = %q, want %q", got.Context.Model, "anthropic/claude-sonnet-4")
	}
}

// --- tokenTotalsFromMessages ---

// makeMsg builds a db.Message with a JSON-encoded data block.
// Mirrors the helper used in the broader DB tests so the shape stays
// in sync with what the adapter actually receives.
func makeMsg(t *testing.T, data map[string]any) db.Message {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return db.Message{ID: "m", SessionID: "s", TimeCreated: 0, Data: raw}
}

func TestTokenTotalsFromMessages(t *testing.T) {
	tests := []struct {
		name string
		in   []db.Message
		want platforms.TokenTotals
	}{
		{
			name: "empty",
			in:   nil,
			want: platforms.TokenTotals{},
		},
		{
			name: "sums input/output across messages",
			in: []db.Message{
				makeMsg(t, map[string]any{
					"role":   "assistant",
					"tokens": map[string]any{"input": 10, "output": 5},
				}),
				makeMsg(t, map[string]any{
					"role":   "assistant",
					"tokens": map[string]any{"input": 7, "output": 3},
				}),
			},
			want: platforms.TokenTotals{Input: 17, Output: 8},
		},
		{
			name: "sums cache.read and cache.write",
			in: []db.Message{
				makeMsg(t, map[string]any{
					"role": "assistant",
					"tokens": map[string]any{
						"input": 1, "output": 1,
						"cache": map[string]any{"read": 100, "write": 50},
					},
				}),
				makeMsg(t, map[string]any{
					"role": "assistant",
					"tokens": map[string]any{
						"input": 1, "output": 1,
						"cache": map[string]any{"read": 5},
					},
				}),
			},
			want: platforms.TokenTotals{Input: 2, Output: 2, CacheRead: 105, CacheWrite: 50},
		},
		{
			name: "skips messages without a tokens payload",
			in: []db.Message{
				makeMsg(t, map[string]any{"role": "user"}),
				makeMsg(t, map[string]any{
					"role":   "assistant",
					"tokens": map[string]any{"input": 4, "output": 2},
				}),
			},
			want: platforms.TokenTotals{Input: 4, Output: 2},
		},
		{
			name: "tolerates malformed message data",
			in: []db.Message{
				{Data: []byte("{not json")},
				makeMsg(t, map[string]any{
					"role":   "assistant",
					"tokens": map[string]any{"input": 9},
				}),
			},
			want: platforms.TokenTotals{Input: 9},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenTotalsFromMessages(tt.in)
			if got != tt.want {
				t.Errorf("got=%+v want=%+v", got, tt.want)
			}
		})
	}
}

// --- costsFromMessages ---

// fakePricing is a CostCalculator stub that ignores the modelID and
// applies a fixed per-token rate. Lets the test assert exact values
// without depending on the LiteLLM table.
type fakePricing struct {
	in, out, cacheR, cacheW float64
}

func (f fakePricing) CalcCost(_ string, in, out, cr, cw int64) float64 {
	return float64(in)*f.in + float64(out)*f.out +
		float64(cr)*f.cacheR + float64(cw)*f.cacheW
}

// approxEqual compares two floats with a tolerance suitable for
// summed-pricing computations (the absolute values stay small —
// dollars or fractions thereof — so a 1e-9 tolerance is plenty).
func approxEqual(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-9
}

func TestCostsFromMessages(t *testing.T) {
	t.Run("sums upstream cost verbatim", func(t *testing.T) {
		// Upstream-only: pricing is nil, so EstCost stays 0. Cost
		// reflects every assistant message's cost field.
		msgs := []db.Message{
			makeMsg(t, map[string]any{"role": "assistant", "cost": 0.30}),
			makeMsg(t, map[string]any{"role": "assistant", "cost": 0.05}),
			makeMsg(t, map[string]any{"role": "user", "cost": 99.99}), // ignored
		}
		cost, est, _ := costsFromMessages(msgs, nil)
		if !approxEqual(cost, 0.35) {
			t.Errorf("cost=%v want=0.35", cost)
		}
		if est != 0 {
			t.Errorf("est=%v want=0 (no pricing)", est)
		}
	})

	t.Run("computes EstCost independently of upstream cost", func(t *testing.T) {
		// API-priced session: both Cost and EstCost are populated.
		// Cost is the upstream sum; EstCost is a pricing-table
		// recomputation from the same tokens. Both must add up
		// without one suppressing the other.
		msgs := []db.Message{
			makeMsg(t, map[string]any{
				"role":       "assistant",
				"cost":       0.42,
				"providerID": "anthropic",
				"modelID":    "claude-sonnet-4",
				"tokens":     map[string]any{"input": 100, "output": 50},
			}),
		}
		// est: 100*0.001 + 50*0.002 = 0.10 + 0.10 = 0.20
		fp := fakePricing{in: 0.001, out: 0.002}
		cost, est, _ := costsFromMessages(msgs, fp)
		if !approxEqual(cost, 0.42) {
			t.Errorf("cost=%v want=0.42", cost)
		}
		if !approxEqual(est, 0.20) {
			t.Errorf("est=%v want=0.20", est)
		}
	})

	t.Run("subscription-plan style: zero Cost, non-zero EstCost", func(t *testing.T) {
		// Every assistant message records cost=0 but has populated
		// tokens. The split lets the panel surface this combination
		// honestly: "Cost: $0.00 / Est: $2.10".
		msgs := []db.Message{
			makeMsg(t, map[string]any{
				"role":       "assistant",
				"providerID": "anthropic",
				"modelID":    "claude-sonnet-4",
				"tokens": map[string]any{
					"input": 1000, "output": 500,
					"cache": map[string]any{"read": 100, "write": 50},
				},
			}),
		}
		// 1000*0.001 + 500*0.002 + 100*0.0005 + 50*0.0010
		fp := fakePricing{in: 0.001, out: 0.002, cacheR: 0.0005, cacheW: 0.0010}
		cost, est, effective := costsFromMessages(msgs, fp)
		if cost != 0 {
			t.Errorf("cost=%v want=0", cost)
		}
		if !approxEqual(est, 2.10) {
			t.Errorf("est=%v want=2.10", est)
		}
		if !approxEqual(effective, 2.10) {
			t.Errorf("effective=%v want=2.10", effective)
		}
	})

	t.Run("uses reported cost per message and estimates only zero-cost messages", func(t *testing.T) {
		msgs := []db.Message{
			makeMsg(t, map[string]any{
				"role": "assistant", "cost": 0.42, "modelID": "model",
				"tokens": map[string]any{"input": 100, "output": 50},
			}),
			makeMsg(t, map[string]any{
				"role": "assistant", "cost": 0, "modelID": "model",
				"tokens": map[string]any{"input": 20, "output": 10},
			}),
		}
		cost, est, effective := costsFromMessages(msgs, fakePricing{in: 0.001, out: 0.002})
		if !approxEqual(cost, 0.42) || !approxEqual(est, 0.24) || !approxEqual(effective, 0.46) {
			t.Errorf("got=(%v,%v,%v) want=(0.42,0.24,0.46)", cost, est, effective)
		}
	})

	t.Run("returns zero/zero when nothing usable is available", func(t *testing.T) {
		// Pricing is nil and every message has cost=0. Both rows
		// will render "$0.00" — the label "Est" on the second row
		// makes the absence honest.
		msgs := []db.Message{
			makeMsg(t, map[string]any{
				"role":   "assistant",
				"tokens": map[string]any{"input": 100, "output": 50},
			}),
		}
		cost, est, _ := costsFromMessages(msgs, nil)
		if cost != 0 || est != 0 {
			t.Errorf("got=(%v,%v) want=(0,0)", cost, est)
		}
	})

	t.Run("EstCost skips messages with no model id", func(t *testing.T) {
		// A pricing calculator can't help if we don't know the model.
		// EstCost stays 0, but Cost still reflects the upstream value.
		msgs := []db.Message{
			makeMsg(t, map[string]any{
				"role":   "assistant",
				"cost":   0.10,
				"tokens": map[string]any{"input": 100, "output": 50},
			}),
		}
		fp := fakePricing{in: 0.001, out: 0.002}
		cost, est, _ := costsFromMessages(msgs, fp)
		if !approxEqual(cost, 0.10) {
			t.Errorf("cost=%v want=0.10", cost)
		}
		if est != 0 {
			t.Errorf("est=%v want=0 (no modelID)", est)
		}
	})

	t.Run("tolerates malformed and non-assistant entries", func(t *testing.T) {
		msgs := []db.Message{
			{Data: []byte("{not json")},
			makeMsg(t, map[string]any{"role": "user", "cost": 99.99}),
			makeMsg(t, map[string]any{"role": "assistant", "cost": 0.10}),
		}
		cost, est, _ := costsFromMessages(msgs, nil)
		if !approxEqual(cost, 0.10) {
			t.Errorf("cost=%v want=0.10", cost)
		}
		if est != 0 {
			t.Errorf("est=%v want=0", est)
		}
	})
}

// --- messageCountsFromMessages ---

func TestMessageCountsFromMessages(t *testing.T) {
	tests := []struct {
		name string
		in   []db.Message
		want platforms.MessageCounts
	}{
		{
			name: "empty",
			in:   nil,
			want: platforms.MessageCounts{},
		},
		{
			name: "counts user and assistant turns",
			in: []db.Message{
				makeMsg(t, map[string]any{"role": "user"}),
				makeMsg(t, map[string]any{"role": "assistant"}),
				makeMsg(t, map[string]any{"role": "user"}),
				makeMsg(t, map[string]any{"role": "assistant"}),
				makeMsg(t, map[string]any{"role": "assistant"}),
			},
			want: platforms.MessageCounts{User: 2, Assistant: 3},
		},
		{
			name: "ignores unknown roles and malformed data",
			in: []db.Message{
				makeMsg(t, map[string]any{"role": "system"}),
				makeMsg(t, map[string]any{"role": "user"}),
				{Data: []byte("{not json")},
				makeMsg(t, map[string]any{"role": "assistant"}),
				{Data: nil},
			},
			want: platforms.MessageCounts{User: 1, Assistant: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := messageCountsFromMessages(tt.in)
			if got != tt.want {
				t.Errorf("got=%+v want=%+v", got, tt.want)
			}
		})
	}
}

// --- latestTodosFromParts ---

// makeTodoPart builds a db.Part whose data is a JSON-encoded
// todowrite tool call. Tool name is configurable so we can exercise
// the full set of accepted variants (snake_case, PascalCase, mcp_*).
func makeTodoPart(t *testing.T, tool string, todos any) db.Part {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"type":  "tool",
		"tool":  tool,
		"state": map[string]any{"input": map[string]any{"todos": todos}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return db.Part{ID: "p", MessageID: "m", SessionID: "s", Data: data}
}

func TestLatestTodosFromParts(t *testing.T) {
	t.Run("returns nil when no todowrite parts exist", func(t *testing.T) {
		parts := []db.Part{
			{Data: mustJSON(t, map[string]any{"type": "tool", "tool": "read"})},
		}
		if got := latestTodosFromParts(parts); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})

	t.Run("returns the most recent list when multiple writes exist", func(t *testing.T) {
		older := makeTodoPart(t, "todowrite", []any{
			map[string]any{"content": "first", "status": "pending", "priority": "medium"},
		})
		newer := makeTodoPart(t, "todowrite", []any{
			map[string]any{"content": "updated", "status": "in_progress", "priority": "high"},
			map[string]any{"content": "next", "status": "pending", "priority": "low"},
		})
		got := latestTodosFromParts([]db.Part{older, newer})
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2: %+v", len(got), got)
		}
		if got[0].Content != "updated" {
			t.Errorf("got[0].Content = %q, want updated", got[0].Content)
		}
	})

	t.Run("matches the PascalCase TodoWrite tool name", func(t *testing.T) {
		part := makeTodoPart(t, "TodoWrite", []any{
			map[string]any{"content": "cc", "status": "pending", "priority": "medium"},
		})
		got := latestTodosFromParts([]db.Part{part})
		if len(got) != 1 || got[0].Content != "cc" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("matches mcp_-prefixed variants", func(t *testing.T) {
		part := makeTodoPart(t, "mcp_todowrite", []any{
			map[string]any{"content": "mcp", "status": "pending", "priority": "medium"},
		})
		got := latestTodosFromParts([]db.Part{part})
		if len(got) != 1 || got[0].Content != "mcp" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("accepts a bare-array input shape", func(t *testing.T) {
		// Some platform versions emit `state.input` as the array
		// directly rather than wrapping it in `{ todos: [...] }`.
		data, _ := json.Marshal(map[string]any{
			"type": "tool",
			"tool": "todowrite",
			"state": map[string]any{
				"input": []any{
					map[string]any{"content": "bare", "status": "pending", "priority": "medium"},
				},
			},
		})
		got := latestTodosFromParts([]db.Part{{Data: data}})
		if len(got) != 1 || got[0].Content != "bare" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("skips empty todo arrays", func(t *testing.T) {
		part := makeTodoPart(t, "todowrite", []any{})
		if got := latestTodosFromParts([]db.Part{part}); got != nil {
			t.Errorf("got %+v, want nil for empty", got)
		}
	})

	t.Run("ignores malformed parts and continues the walk", func(t *testing.T) {
		// A malformed part newer than a valid one should not abort —
		// the walk continues to the next-newest valid todowrite.
		valid := makeTodoPart(t, "todowrite", []any{
			map[string]any{"content": "fallback", "status": "pending", "priority": "medium"},
		})
		bad := db.Part{Data: []byte("{not json")}
		got := latestTodosFromParts([]db.Part{valid, bad})
		if len(got) != 1 || got[0].Content != "fallback" {
			t.Errorf("got %+v", got)
		}
	})
}

// mustJSON is a tiny helper used by tests that don't need the
// makeTodoPart tool-name flexibility.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// --- computeAlwaysOnTier ---
//
// Single source of truth for the per-session "always-on" data
// SessionInfo renders (token totals, user/assistant counts, todos,
// upstream + estimated cost, latest context-window tokens). Driven
// from either the read-only DB or live HTTP responses depending on
// availability — the pure helper makes the choice testable in
// isolation.

func TestComputeAlwaysOnTier(t *testing.T) {
	t.Run("aggregates tokens, counts, cost, and context tokens", func(t *testing.T) {
		messages := []db.Message{
			makeMsg(t, map[string]any{
				"role":   "user",
				"tokens": map[string]any{"input": 1, "output": 0}, // ignored: user role has no contextual cost
			}),
			makeMsg(t, map[string]any{
				"role":       "assistant",
				"cost":       0.10,
				"providerID": "anthropic",
				"modelID":    "claude-sonnet-4",
				"tokens": map[string]any{
					"input": 100, "output": 50, "reasoning": 5,
					"cache": map[string]any{"read": 200, "write": 30},
				},
			}),
		}
		// One todowrite part — the panel surfaces the latest list.
		parts := []db.Part{
			makeTodoPart(t, "todowrite", []any{
				map[string]any{"content": "hello", "status": "pending", "priority": "medium"},
			}),
		}
		fp := fakePricing{in: 0.001, out: 0.002}

		got := computeAlwaysOnTier(messages, parts, fp)

		// Tokens: cache.read/cache.write only sum from messages whose
		// `tokens` payload includes them. The user message has no
		// cache block; the assistant one does.
		if got.tokens != (platforms.TokenTotals{Input: 101, Output: 50, CacheRead: 200, CacheWrite: 30}) {
			t.Errorf("tokens = %+v", got.tokens)
		}
		if got.messages != (platforms.MessageCounts{User: 1, Assistant: 1}) {
			t.Errorf("messages = %+v", got.messages)
		}
		if !approxEqual(got.cost, 0.10) {
			t.Errorf("cost = %v want 0.10", got.cost)
		}
		// est: 100*0.001 + 50*0.002 = 0.20
		if !approxEqual(got.estCost, 0.20) {
			t.Errorf("estCost = %v want 0.20", got.estCost)
		}
		// ctxTokens = input+output+reasoning+cache.read+cache.write of
		// the last assistant message with output > 0.
		if got.ctxTokens != 100+50+5+200+30 {
			t.Errorf("ctxTokens = %d want 385", got.ctxTokens)
		}
		if len(got.todos) != 1 || got.todos[0].Content != "hello" {
			t.Errorf("todos = %+v", got.todos)
		}
	})

	t.Run("empty inputs yield zero values", func(t *testing.T) {
		got := computeAlwaysOnTier(nil, nil, nil)
		if got.tokens != (platforms.TokenTotals{}) ||
			got.messages != (platforms.MessageCounts{}) ||
			got.cost != 0 || got.estCost != 0 ||
			got.ctxTokens != 0 || got.todos != nil {
			t.Errorf("non-zero from empty input: %+v", got)
		}
	})

	t.Run("ctxTokens uses the last assistant message with output>0", func(t *testing.T) {
		// A trailing assistant message with output=0 (e.g. a turn
		// that errored before completion) must not overwrite the
		// previous valid context-window snapshot.
		messages := []db.Message{
			makeMsg(t, map[string]any{
				"role": "assistant",
				"tokens": map[string]any{
					"input": 10, "output": 5,
				},
			}),
			makeMsg(t, map[string]any{
				"role": "assistant",
				"tokens": map[string]any{
					"input": 99, "output": 0, // skipped
				},
			}),
		}
		got := computeAlwaysOnTier(messages, nil, nil)
		if got.ctxTokens != 15 {
			t.Errorf("ctxTokens = %d want 15", got.ctxTokens)
		}
	})
}
