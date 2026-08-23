package opencode

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/srvtiming"
)

// rawMCPEntry mirrors a single value in OpenCode's `GET /mcp`
// response. The wire format is `{name: {status, error?}}`; we don't
// translate the status string — the frontend renders whatever the
// upstream emits ("connected" / "needs_auth" / "failed" / future
// values), per the verbatim contract documented on
// platforms.MCPServer.
type rawMCPEntry struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// rawLSPEntry mirrors one entry from OpenCode's `GET /lsp` response.
// The full payload also carries `root` (the directory the LSP
// attached to) which we drop here — the panel doesn't surface it and
// keeping the type narrow makes the boundary easier to evolve.
type rawLSPEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// SessionInfo collects the per-session info snapshot consumed by the
// "Session info" right-hand panel.
//
// Two tiers of work:
//
//   - Always: the lifetime token totals (input/output/cache.read/
//     cache.write) and the latest todowrite list. Both come from the
//     read-only DB so they're available even when no OpenCode
//     instance is currently running for this session's directory.
//
//   - Live (only when a port is discovered): the per-model context
//     window, configured MCP servers + status, and configured LSP
//     servers + status. These require an HTTP round-trip to the
//     running OpenCode instance.
//
// `Supported` reflects the live tier specifically: when false, the
// frontend hides the live sections but still renders the always-on
// data (tokens / todos) and the cross-platform Session metadata
// (project / branch / messages / duration / changes / cost) it gets
// from the regular session payload.
func (a *Adapter) SessionInfo(ctx context.Context, sessionID string) (*platforms.SessionInfo, error) {
	if a.db == nil {
		return nil, platforms.ErrNotFound
	}
	dbSession, err := a.db.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	defaults, _ := getSessionDefaultsCached(ctx, a.db, sessionID, dbSession.Directory)
	modelRef := defaults.Model

	port := resolveOpenCodePortForSessionCtx(ctx, sessionID, dbSession.Directory)
	if port == "" {
		// No live channel — compute the always-on tier from the
		// read-only DB and return Supported=false so the frontend
		// hides MCP/LSP/context-window while still rendering tokens
		// and todos.
		tier := alwaysOnTierFromDB(ctx, a.db, sessionID, a.pricing)
		return &platforms.SessionInfo{
			SessionID:  sessionID,
			Supported:  false,
			Tokens:     tier.tokens,
			Messages:   tier.messages,
			Todos:      tier.todos,
			MCPServers: []platforms.MCPServer{},
			LSPServers: []platforms.LSPServer{},
			Context: platforms.ContextInfo{
				Tokens:  tier.ctxTokens,
				Cost:    tier.cost,
				EstCost: tier.estCost,
				Model:   modelRef,
			},
		}, nil
	}

	// Live tier: fan out four independent fetches in parallel. Each
	// failure is tolerated — the builder fills in zero values for
	// whatever we couldn't fetch.
	//
	// The fourth slot used to fetch /session/{id}/message *just for*
	// the context-token rollup, on top of an unconditional DB read
	// for the other always-on data (tokens / messages / todos / cost).
	// We now reuse that single live fetch as the source of truth for
	// the entire always-on tier, falling back to the DB only if the
	// live fetch fails. Net effect: live-path SessionInfo drops three
	// DB queries (GetSessionMessages, GetSessionParts,
	// GetContextTokenCount) and goes from "DB walk + live walk" to
	// "live walk only".
	var (
		mcp      map[string]rawMCPEntry
		lsp      []rawLSPEntry
		prov     OpenCodeProvidersResponse
		hasPrv   bool
		liveOK   bool
		liveTier alwaysOnTier
		wg       sync.WaitGroup
	)
	parallelPhase := srvtiming.Begin(ctx, "http_parallel")
	wg.Add(4)
	go func() {
		defer wg.Done()
		p := srvtiming.Begin(ctx, "http_mcp")
		mcp = fetchOpenCodeMCP(port)
		p.EndWithDesc("GET /mcp")
	}()
	go func() {
		defer wg.Done()
		p := srvtiming.Begin(ctx, "http_lsp")
		lsp = fetchOpenCodeLSP(port)
		p.EndWithDesc("GET /lsp")
	}()
	go func() {
		defer wg.Done()
		p := srvtiming.Begin(ctx, "http_provider")
		prov, hasPrv = fetchOpenCodeProviders(port)
		p.EndWithDesc("GET /provider")
	}()
	go func() {
		defer wg.Done()
		p := srvtiming.Begin(ctx, "live_tier")
		liveTier, liveOK = alwaysOnTierFromOpenCode(port, sessionID, a.pricing)
		p.EndWithDesc("GET /session/{id}/message + aggregate")
	}()
	wg.Wait()
	parallelPhase.EndWithDesc("info 4-way fan-out")

	var provPtr *OpenCodeProvidersResponse
	if hasPrv {
		provPtr = &prov
	}
	// Pick the tier we'll surface. The live fetch is preferred when
	// it succeeds; on upstream failure we fall back to the DB so
	// transient OpenCode problems don't blank the panel.
	tier := liveTier
	if !liveOK {
		tier = alwaysOnTierFromDB(ctx, a.db, sessionID, a.pricing)
	}

	info := buildSessionInfo(sessionID, tier.ctxTokens, tier.cost, tier.estCost, modelRef, mcp, lsp, provPtr)
	info.Tokens = tier.tokens
	info.Messages = tier.messages
	info.Todos = tier.todos
	return info, nil
}

// alwaysOnTier collects the per-session data the SessionInfo panel
// renders regardless of whether an OpenCode instance is currently
// running. Carried as a struct so callers can swap the DB and live
// sources interchangeably.
type alwaysOnTier struct {
	tokens    platforms.TokenTotals
	messages  platforms.MessageCounts
	todos     []platforms.TodoItem
	cost      float64 // upstream-recorded cost (data.cost field)
	estCost   float64 // pricing-table recompute from token counts
	ctxTokens int64   // last assistant message with output>0: in+out+reason+cache
}

// computeAlwaysOnTier is the pure aggregator that turns a session's
// messages and parts into the always-on tier. Both the DB and live
// HTTP code paths funnel through this function — the only thing that
// varies is where messages/parts come from. Keeps the per-source
// branches at the call site free of aggregation logic.
func computeAlwaysOnTier(messages []db.Message, parts []db.Part, pricing CostCalculator) alwaysOnTier {
	cost, estCost, _ := costsFromMessages(messages, pricing)
	return alwaysOnTier{
		tokens:    tokenTotalsFromMessages(messages),
		messages:  messageCountsFromMessages(messages),
		todos:     latestTodosFromParts(parts),
		cost:      cost,
		estCost:   estCost,
		ctxTokens: contextTokensFromMessages(messages),
	}
}

// contextTokensFromMessages returns the same value GetContextTokenCount
// reads from SQLite: the input+output+reasoning+cache.read+cache.write
// sum on the most recent assistant message that actually produced
// output. Returns 0 when no such message exists.
//
// Walks newest-to-oldest so a trailing assistant message with output=0
// (e.g. an errored turn) doesn't overwrite the prior valid snapshot.
func contextTokensFromMessages(messages []db.Message) int64 {
	for i := len(messages) - 1; i >= 0; i-- {
		if len(messages[i].Data) == 0 {
			continue
		}
		var probe struct {
			Role   string `json:"role"`
			Tokens *struct {
				Input     int64 `json:"input"`
				Output    int64 `json:"output"`
				Reasoning int64 `json:"reasoning"`
				Cache     *struct {
					Read  int64 `json:"read"`
					Write int64 `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		}
		if err := json.Unmarshal(messages[i].Data, &probe); err != nil {
			continue
		}
		if probe.Role != "assistant" || probe.Tokens == nil || probe.Tokens.Output <= 0 {
			continue
		}
		total := probe.Tokens.Input + probe.Tokens.Output + probe.Tokens.Reasoning
		if probe.Tokens.Cache != nil {
			total += probe.Tokens.Cache.Read + probe.Tokens.Cache.Write
		}
		return total
	}
	return 0
}

// alwaysOnTierFromDB pulls messages + parts from the read-only DB
// and runs computeAlwaysOnTier. DB errors are non-fatal (the panel
// still renders, just with zero values for whatever failed) which
// matches the prior best-effort behaviour.
func alwaysOnTierFromDB(ctx context.Context, database *db.DB, sessionID string, pricing CostCalculator) alwaysOnTier {
	messages, _ := database.GetSessionMessages(ctx, sessionID)
	parts, _ := database.GetSessionParts(ctx, sessionID)
	return computeAlwaysOnTier(messages, parts, pricing)
}

// alwaysOnTierFromOpenCode fetches the running session's full message
// history once over HTTP, converts it to the typed Message+Part shape
// the aggregators expect, and runs computeAlwaysOnTier. Returns
// (zero, false) on any upstream failure so the caller falls back to
// the DB.
//
// This is the consolidated single fetch that replaces the legacy
// pattern (DB walk for tokens/messages/todos/cost + a separate live
// fetch only for the context-token rollup). One round-trip, one walk.
func alwaysOnTierFromOpenCode(port, sessionID string, pricing CostCalculator) (alwaysOnTier, bool) {
	raw, err := fetchOpenCodeMessages(port, sessionID)
	if err != nil {
		return alwaysOnTier{}, false
	}
	untypedMsgs, untypedParts := convertOpenCodeMessages(raw)
	messages := typedMessagesFromUntyped(untypedMsgs)
	parts := typedPartsFromUntyped(untypedParts)
	return computeAlwaysOnTier(messages, parts, pricing), true
}

// tokenTotalsFromMessages sums the lifetime token usage for a session
// across the four buckets the SessionInfo panel surfaces. Mirrors
// computeMessageStats's accumulation logic but emits the wider shape
// (cache.read / cache.write are not summed by the existing
// /api/sessions DB query).
//
// Tolerant about message.Data being null or missing fields; messages
// with no `tokens` payload are skipped.
func tokenTotalsFromMessages(messages []db.Message) platforms.TokenTotals {
	var totals platforms.TokenTotals
	for _, m := range messages {
		if len(m.Data) == 0 {
			continue
		}
		var probe struct {
			Tokens *struct {
				Input  int64 `json:"input"`
				Output int64 `json:"output"`
				Cache  *struct {
					Read  int64 `json:"read"`
					Write int64 `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		}
		if err := json.Unmarshal(m.Data, &probe); err != nil {
			continue
		}
		if probe.Tokens == nil {
			continue
		}
		totals.Input += probe.Tokens.Input
		totals.Output += probe.Tokens.Output
		if probe.Tokens.Cache != nil {
			totals.CacheRead += probe.Tokens.Cache.Read
			totals.CacheWrite += probe.Tokens.Cache.Write
		}
	}
	return totals
}

// costsFromMessages walks every assistant message and returns reported,
// estimated, and effective cost totals:
//
//   - Cost: sum of the upstream `data.cost` field across assistant
//     messages. This is what the platform itself recorded — the
//     amount actually billed for API-priced sessions, and 0 for
//     subscription-plan accounts where the API was hit but the
//     message metadata records cost=0.
//
//   - EstCost: sum of `pricing.CalcCost(modelRef, tokens...)` across
//     the same messages. Always derived from token counts and the
//     loaded pricing table — never reads the upstream cost field.
//     Useful as a sanity check against Cost (large mismatches usually
//     mean an unrecognised model name) and as the only meaningful
//     number for subscription-plan sessions.
//
//   - EffectiveCost: per message, uses Cost when non-zero and EstCost
//     otherwise. This preserves reported billing in mixed sessions while
//     filling gaps from subscription-plan turns.
//
// Splitting the two lets the UI render both "Cost" and "Est" as
// adjacent rows, so the user can spot a $0 / non-zero pair (the
// hallmark of a subscription-plan session) at a glance instead of
// the panel silently substituting one for the other.
//
// EstCost is 0 when pricing is nil. The model id used for lookup is
// the message's `providerID/modelID` if both are present, falling
// back to just modelID — same convention internal/db/sessions.go
// uses elsewhere.
func costsFromMessages(messages []db.Message, pricing CostCalculator) (cost, estCost, effectiveCost float64) {
	for _, m := range messages {
		if len(m.Data) == 0 {
			continue
		}
		var probe struct {
			Role       string  `json:"role"`
			Cost       float64 `json:"cost"`
			ProviderID string  `json:"providerID"`
			ModelID    string  `json:"modelID"`
			Tokens     *struct {
				Input  int64 `json:"input"`
				Output int64 `json:"output"`
				Cache  *struct {
					Read  int64 `json:"read"`
					Write int64 `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		}
		if err := json.Unmarshal(m.Data, &probe); err != nil {
			continue
		}
		if probe.Role != "assistant" {
			continue
		}
		// Cost: sum upstream verbatim.
		cost += probe.Cost
		// Est: independent, always recomputes from tokens via the
		// pricing table. Skip messages with no model or no tokens —
		// CalcCost would just return 0.
		if pricing == nil || probe.Tokens == nil {
			effectiveCost += probe.Cost
			continue
		}
		modelRef := probe.ModelID
		if probe.ProviderID != "" && probe.ModelID != "" {
			modelRef = probe.ProviderID + "/" + probe.ModelID
		}
		if modelRef == "" {
			effectiveCost += probe.Cost
			continue
		}
		var cacheRead, cacheWrite int64
		if probe.Tokens.Cache != nil {
			cacheRead = probe.Tokens.Cache.Read
			cacheWrite = probe.Tokens.Cache.Write
		}
		estimated := pricing.CalcCost(
			modelRef,
			probe.Tokens.Input, probe.Tokens.Output,
			cacheRead, cacheWrite,
		)
		estCost += estimated
		if probe.Cost > 0 {
			effectiveCost += probe.Cost
		} else {
			effectiveCost += estimated
		}
	}
	return cost, estCost, effectiveCost
}

// messageCountsFromMessages counts user vs assistant turns in the
// session. The /api/sessions wire payload's `messageCount` field
// only counts user messages (see GetSessions's SQL — the `role =
// 'user'` filter), which is fine for the dashboard but loses
// information the SessionInfo panel wants to render as "user + agent".
//
// Tolerant of malformed data the same way tokenTotalsFromMessages is:
// a message whose JSON fails to parse, or whose role is anything
// other than "user" / "assistant", is skipped silently.
func messageCountsFromMessages(messages []db.Message) platforms.MessageCounts {
	var counts platforms.MessageCounts
	for _, m := range messages {
		if len(m.Data) == 0 {
			continue
		}
		var probe struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(m.Data, &probe); err != nil {
			continue
		}
		switch probe.Role {
		case "user":
			counts.User++
		case "assistant":
			counts.Assistant++
		}
	}
	return counts
}

// todoToolNames is the set of tool names that represent a todowrite
// invocation across platforms. We support both the snake_case and
// PascalCase variants (plus their MCP-prefixed forms) so the panel
// works against any session.
var todoToolNames = map[string]struct{}{
	"todowrite":     {},
	"TodoWrite":     {},
	"mcp_todowrite": {},
	"mcp_TodoWrite": {},
}

// latestTodosFromParts walks the parts list newest-to-oldest and
// returns the most recent recognisable todo list, or nil if none.
// The todowrite tool replaces the entire list on every call, so the
// latest invocation is the live state of the conversation's task
// tracker.
//
// Tolerant about malformed parts — a single part whose data fails to
// parse is skipped rather than aborting the whole walk.
func latestTodosFromParts(parts []db.Part) []platforms.TodoItem {
	for i := len(parts) - 1; i >= 0; i-- {
		var pd struct {
			Type  string `json:"type"`
			Tool  string `json:"tool"`
			State *struct {
				Input json.RawMessage `json:"input"`
			} `json:"state"`
		}
		if err := json.Unmarshal(parts[i].Data, &pd); err != nil {
			continue
		}
		if pd.Type != "tool" {
			continue
		}
		if _, ok := todoToolNames[pd.Tool]; !ok {
			continue
		}
		if pd.State == nil || len(pd.State.Input) == 0 {
			continue
		}
		todos := parseTodoList(pd.State.Input)
		if len(todos) > 0 {
			return todos
		}
	}
	return nil
}

// parseTodoList accepts the `state.input` payload of a todowrite tool
// call (which can be either `{"todos":[...]}` or just the array) and
// returns the contained TodoItems. Returns nil for any other shape.
func parseTodoList(raw json.RawMessage) []platforms.TodoItem {
	// Try `{ todos: [...] }` first — that's the canonical OpenCode
	// shape.
	var obj struct {
		Todos []platforms.TodoItem `json:"todos"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && len(obj.Todos) > 0 {
		return obj.Todos
	}
	// Fallback: bare array.
	var arr []platforms.TodoItem
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		return arr
	}
	return nil
}

// buildSessionInfo folds the raw HTTP responses into a SessionInfo.
// Pure: no IO, no globals — easy to test.
//
// Defensive about nil/empty inputs: a missing /mcp or /lsp response
// becomes an empty (non-nil) slice. A missing provider catalog leaves
// Context.Limit at zero — the frontend treats that as "unknown" and
// hides the percent-used calculation rather than displaying a
// fabricated number.
func buildSessionInfo(
	sessionID string,
	tokens int64,
	cost, estCost float64,
	modelRef string,
	mcp map[string]rawMCPEntry,
	lsp []rawLSPEntry,
	prov *OpenCodeProvidersResponse,
) *platforms.SessionInfo {
	info := &platforms.SessionInfo{
		SessionID: sessionID,
		Supported: true,
		Context: platforms.ContextInfo{
			Tokens:  tokens,
			Cost:    cost,
			EstCost: estCost,
			Model:   modelRef,
			Limit:   contextLimitFor(modelRef, prov),
		},
		MCPServers: make([]platforms.MCPServer, 0, len(mcp)),
		LSPServers: make([]platforms.LSPServer, 0, len(lsp)),
	}

	for name, entry := range mcp {
		s := platforms.MCPServer{
			Name:   name,
			Status: entry.Status,
			Error:  entry.Error,
		}
		if entry.Status == "needs_auth" {
			s.AuthHint = "opencode mcp auth " + name
		}
		info.MCPServers = append(info.MCPServers, s)
	}
	// Stable order: MCP comes off the wire as a JSON object (no
	// inherent order). Alphabetical by name is predictable and matches
	// how the OpenCode TUI renders the same list.
	sort.Slice(info.MCPServers, func(i, j int) bool {
		return info.MCPServers[i].Name < info.MCPServers[j].Name
	})

	for _, entry := range lsp {
		info.LSPServers = append(info.LSPServers, platforms.LSPServer{
			ID:     entry.ID,
			Name:   entry.Name,
			Status: entry.Status,
		})
	}
	return info
}

// contextLimitFor looks up the context-window size of a model in the
// OpenCode providers catalog. modelRef is the "provider/model" form
// already in use by the rest of the adapter (DefaultModel,
// SessionModels, …). Returns 0 when the catalog is missing, the ref
// is malformed, or the model isn't in the catalog — the frontend
// treats 0 as "unknown" and hides the % used line.
func contextLimitFor(modelRef string, prov *OpenCodeProvidersResponse) int64 {
	if prov == nil || modelRef == "" {
		return 0
	}
	slash := strings.IndexByte(modelRef, '/')
	if slash <= 0 || slash == len(modelRef)-1 {
		return 0
	}
	providerID := modelRef[:slash]
	modelID := modelRef[slash+1:]
	for _, p := range prov.All {
		if p.ID != providerID {
			continue
		}
		m, ok := p.Models[modelID]
		if !ok {
			return 0
		}
		return m.Limit.Context
	}
	return 0
}

// fetchOpenCodeMCP calls GET /mcp on the running OpenCode instance.
// Returns an empty map (not nil) on any failure so callers can rely on
// `len(mcp) == 0` to mean "no servers configured" without distinguishing
// "no servers" from "couldn't ask". The caller surfaces that ambiguity
// at panel level — when the port is unreachable we already return
// ErrUnsupported earlier.
func fetchOpenCodeMCP(port string) map[string]rawMCPEntry {
	out := map[string]rawMCPEntry{}
	if !getInto(port, "/mcp", &out) {
		return map[string]rawMCPEntry{}
	}
	return out
}

// fetchOpenCodeLSP calls GET /lsp on the running OpenCode instance
// and returns the configured language servers. Returns an empty slice
// on failure for the same reason as fetchOpenCodeMCP.
func fetchOpenCodeLSP(port string) []rawLSPEntry {
	out := []rawLSPEntry{}
	if !getInto(port, "/lsp", &out) {
		return []rawLSPEntry{}
	}
	return out
}
