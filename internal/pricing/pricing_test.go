package pricing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

// fixedTable returns a *Table populated with a small known fixture so
// tests don't depend on the upstream LiteLLM JSON shape.
func fixedTable(prices map[string]ModelPrice) *Table {
	t := &Table{prices: make(map[string]ModelPrice, len(prices))}
	for k, v := range prices {
		t.prices[k] = v
	}
	t.rebuildKeys()
	return t
}

func TestLookup_ExactMatch(t *testing.T) {
	tbl := fixedTable(map[string]ModelPrice{
		"gpt-4":       {InputPerToken: 1},
		"gpt-4-turbo": {InputPerToken: 2},
		"gpt-4o":      {InputPerToken: 3},
	})

	got := tbl.Lookup("gpt-4")
	if got.InputPerToken != 1 {
		t.Fatalf("Lookup(gpt-4) = %+v, want exact match for gpt-4 (InputPerToken=1)", got)
	}
}

func TestLookup_VersionedModelMapsToBase(t *testing.T) {
	tbl := fixedTable(map[string]ModelPrice{
		"gpt-4":       {InputPerToken: 1},
		"gpt-4-turbo": {InputPerToken: 2},
	})

	got := tbl.Lookup("gpt-4-turbo-2024-04-09")
	if got.InputPerToken != 2 {
		t.Fatalf("Lookup(gpt-4-turbo-2024-04-09) = %+v, want gpt-4-turbo entry (InputPerToken=2)", got)
	}
}

func TestLookup_LongerSpecificWinsForSpecificQuery(t *testing.T) {
	// When the query is "gpt-4o-mini-2024" and the table has both
	// "gpt-4o" and "gpt-4o-mini", we should prefer "gpt-4o-mini" — the
	// longer prefix wins.
	tbl := fixedTable(map[string]ModelPrice{
		"gpt-4o":      {InputPerToken: 1},
		"gpt-4o-mini": {InputPerToken: 2},
	})

	got := tbl.Lookup("gpt-4o-mini-2024-07-18")
	if got.InputPerToken != 2 {
		t.Fatalf("Lookup(gpt-4o-mini-2024-07-18) = %+v, want gpt-4o-mini entry (InputPerToken=2)", got)
	}
}

func TestLookup_ProviderPrefixStripped(t *testing.T) {
	tbl := fixedTable(map[string]ModelPrice{
		"claude-opus-4-5": {InputPerToken: 7},
	})
	got := tbl.Lookup("anthropic/claude-opus-4-5")
	if got.InputPerToken != 7 {
		t.Fatalf("Lookup with provider prefix = %+v, want match", got)
	}
}

func TestLookup_NoMatchReturnsZero(t *testing.T) {
	tbl := fixedTable(map[string]ModelPrice{
		"gpt-4": {InputPerToken: 1},
	})
	got := tbl.Lookup("totally-unknown-model-xyz")
	if got != (ModelPrice{}) {
		t.Fatalf("Lookup(unknown) = %+v, want zero ModelPrice", got)
	}
}

func TestLookup_NilTableReturnsZero(t *testing.T) {
	var tbl *Table
	if got := tbl.Lookup("anything"); got != (ModelPrice{}) {
		t.Fatalf("Lookup on nil = %+v, want zero", got)
	}
}

func TestLookup_EmptyQueryReturnsZero(t *testing.T) {
	tbl := fixedTable(map[string]ModelPrice{"gpt-4": {InputPerToken: 1}})
	if got := tbl.Lookup(""); got != (ModelPrice{}) {
		t.Fatalf("Lookup('') = %+v, want zero", got)
	}
}

func TestCalcCost(t *testing.T) {
	tbl := fixedTable(map[string]ModelPrice{
		"m": {
			InputPerToken:      0.001,
			OutputPerToken:     0.002,
			CacheReadPerToken:  0.0001,
			CacheWritePerToken: 0.0005,
		},
	})

	cases := []struct {
		name                                string
		in, out, cacheRead, cacheWrite      int64
		want                                float64
	}{
		{"all zero", 0, 0, 0, 0, 0},
		{"only input", 1000, 0, 0, 0, 1.0},
		{"only output", 0, 1000, 0, 0, 2.0},
		{"only cache read", 0, 0, 1000, 0, 0.1},
		{"only cache write", 0, 0, 0, 1000, 0.5},
		{"mixed", 1000, 500, 100, 200, 1 + 1 + 0.01 + 0.1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tbl.CalcCost("m", tc.in, tc.out, tc.cacheRead, tc.cacheWrite)
			if !floatNear(got, tc.want, 1e-9) {
				t.Fatalf("CalcCost(%+v) = %v, want %v", tc, got, tc.want)
			}
		})
	}
}

func TestCalcCost_UnknownModelReturnsZero(t *testing.T) {
	tbl := fixedTable(map[string]ModelPrice{"m": {InputPerToken: 1}})
	if got := tbl.CalcCost("not-there", 100, 100, 0, 0); got != 0 {
		t.Fatalf("CalcCost(unknown) = %v, want 0", got)
	}
}

func floatNear(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

// ---------------------------------------------------------------------
// fetch / Load — uses httptest so no real network call.
// ---------------------------------------------------------------------

func TestFetch_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"gpt-4": {"mode":"chat","input_cost_per_token":0.00003,"output_cost_per_token":0.00006},
			"claude-opus-4-5": {"mode":"chat","input_cost_per_token":0.00001,"output_cost_per_token":0.00002,"cache_read_input_token_cost":0.000001,"cache_creation_input_token_cost":0.000005},
			"text-embed": {"mode":"embedding","input_cost_per_token":0.0001},
			"zero-cost": {"mode":"chat","input_cost_per_token":0,"output_cost_per_token":0}
		}`))
	}))
	defer srv.Close()

	tbl := New(srv.Client(), srv.URL)
	if err := tbl.LoadCtx(context.Background()); err != nil {
		t.Fatalf("LoadCtx: %v", err)
	}

	if p := tbl.Lookup("gpt-4"); p.InputPerToken != 0.00003 {
		t.Fatalf("gpt-4 InputPerToken = %v, want 0.00003", p.InputPerToken)
	}
	if p := tbl.Lookup("claude-opus-4-5"); p.CacheReadPerToken != 0.000001 {
		t.Fatalf("claude cache read = %v, want 0.000001", p.CacheReadPerToken)
	}
	// Embedding mode should be skipped.
	if p := tbl.Lookup("text-embed"); p != (ModelPrice{}) {
		t.Fatalf("embedding model loaded; should be skipped: %+v", p)
	}
	// Zero-cost entries should be skipped.
	if p := tbl.Lookup("zero-cost"); p != (ModelPrice{}) {
		t.Fatalf("zero-cost model loaded; should be skipped: %+v", p)
	}
}

func TestFetch_MalformedJSONReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	tbl := New(srv.Client(), srv.URL)
	err := tbl.LoadCtx(context.Background())
	if err == nil {
		t.Fatalf("LoadCtx with malformed JSON: want error, got nil")
	}
	if got := tbl.Lookup("anything"); got != (ModelPrice{}) {
		t.Fatalf("table populated despite error: %+v", got)
	}
}

func TestFetch_HTTP500ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	tbl := New(srv.Client(), srv.URL)
	err := tbl.LoadCtx(context.Background())
	if err == nil {
		t.Fatalf("LoadCtx with HTTP 500: want error, got nil")
	}
}

func TestFetch_NetworkErrorReturnsError(t *testing.T) {
	// Server that closes immediately so the GET fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // close right away — connections will fail.

	tbl := New(srv.Client(), srv.URL)
	if err := tbl.LoadCtx(context.Background()); err == nil {
		t.Fatalf("LoadCtx against closed server: want error, got nil")
	}
}

// FR-3: a fetch failure must be observable via a logrus WARN.
func TestLoad_FetchFailureLogsWarning(t *testing.T) {
	hook := logtest.NewLocal(logrus.StandardLogger())
	defer logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	tbl := New(srv.Client(), srv.URL)
	_ = tbl.LoadCtx(context.Background())

	var found bool
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.WarnLevel && contains(e.Message, "pricing") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a WARN log line mentioning 'pricing'; got %d entries", len(hook.AllEntries()))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (substr == "" || indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
