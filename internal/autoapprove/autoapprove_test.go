package autoapprove

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// TestClaimAutoApprove verifies the in-flight dedup behaviour: the
// first claim for a (session, permission) pair succeeds; subsequent
// claims return ok=false until releaseAutoApprove runs.
func TestClaimAutoApprove(t *testing.T) {
	s := &Service{autoApprove: make(map[string]*autoApproveStatus)}

	ctx1, ok := s.claimAutoApprove(context.Background(), "ses-1", "perm-1")
	if !ok {
		t.Fatalf("first claim should succeed")
	}
	if ctx1 == nil {
		t.Fatalf("claim returned nil context")
	}
	if _, ok := s.claimAutoApprove(context.Background(), "ses-1", "perm-1"); ok {
		t.Errorf("duplicate claim should return ok=false")
	}
	// Different permission ID is independent.
	if _, ok := s.claimAutoApprove(context.Background(), "ses-1", "perm-2"); !ok {
		t.Errorf("different permissionID should be allowed")
	}
	// Different session ID is independent.
	if _, ok := s.claimAutoApprove(context.Background(), "ses-2", "perm-1"); !ok {
		t.Errorf("different sessionID should be allowed")
	}

	// Release the original claim → re-claim must succeed and the
	// previous context must be cancelled.
	s.releaseAutoApprove("ses-1", "perm-1")
	if ctx1.Err() == nil {
		t.Errorf("release should cancel the claim's context")
	}
	if _, ok := s.claimAutoApprove(context.Background(), "ses-1", "perm-1"); !ok {
		t.Errorf("after release, the same permission should be allowed again")
	}
}

// TestClaimAutoApproveConcurrent stresses the dedup under heavy parallel
// load to catch any locking bug.
func TestClaimAutoApproveConcurrent(t *testing.T) {
	s := &Service{autoApprove: make(map[string]*autoApproveStatus)}
	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	var winners int32
	var winnersMu sync.Mutex
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if _, ok := s.claimAutoApprove(context.Background(), "ses-x", "perm-x"); ok {
				winnersMu.Lock()
				winners++
				winnersMu.Unlock()
			}
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Errorf("exactly one goroutine should win the claim race, got %d", winners)
	}
}

// TestCancelAutoApprove verifies Cancel cancels the
// claimed context, returns true when something was cancelled and
// false otherwise, and that the entry survives until release (so
// double-cancel doesn't allow a new claimant to slip in).
func TestCancelAutoApprove(t *testing.T) {
	s := &Service{autoApprove: make(map[string]*autoApproveStatus)}

	if s.Cancel("ses-1", "perm-1") {
		t.Errorf("cancel with no claim should return false")
	}

	ctx, ok := s.claimAutoApprove(context.Background(), "ses-1", "perm-1")
	if !ok {
		t.Fatalf("claim failed")
	}
	if !s.Cancel("ses-1", "perm-1") {
		t.Errorf("cancel of live claim should return true")
	}
	if ctx.Err() == nil {
		t.Errorf("cancel should propagate to the claim's context")
	}
	// Entry still present — a re-claim must fail until the goroutine
	// finishes its work and calls release. This prevents a race where
	// a new permission with the same id arrives mid-cancel and gets
	// silently dropped.
	if _, ok := s.claimAutoApprove(context.Background(), "ses-1", "perm-1"); ok {
		t.Errorf("re-claim before release should be blocked")
	}

	// Double-cancel is a no-op (returns true because the entry is
	// still there; idempotent in effect — the ctx is already cancelled).
	s.Cancel("ses-1", "perm-1")

	// Release frees the slot.
	s.releaseAutoApprove("ses-1", "perm-1")
	if _, ok := s.claimAutoApprove(context.Background(), "ses-1", "perm-1"); !ok {
		t.Errorf("re-claim after release should succeed")
	}
}

// TestJudgedPermissionsCache verifies that recordJudged / lookupJudged
// roundtrip correctly and that the key is (sessionID, permissionID).
func TestJudgedPermissionsCache(t *testing.T) {
	s := &Service{autoApprove: make(map[string]*autoApproveStatus)}

	if _, ok := s.lookupJudged("ses-1", "perm-1"); ok {
		t.Errorf("lookup before record should miss")
	}

	s.recordJudged("ses-1", "perm-1", verdictUnsafe)
	v, ok := s.lookupJudged("ses-1", "perm-1")
	if !ok || v != verdictUnsafe {
		t.Errorf("lookup after record: got (%q, %v), want (unsafe, true)", v, ok)
	}

	// Different permissionID is independent.
	if _, ok := s.lookupJudged("ses-1", "perm-2"); ok {
		t.Errorf("different permissionID should miss")
	}
	// Different sessionID is independent.
	if _, ok := s.lookupJudged("ses-2", "perm-1"); ok {
		t.Errorf("different sessionID should miss")
	}

	// Re-record overwrites (e.g. if we ever evolve to allow re-judging).
	s.recordJudged("ses-1", "perm-1", verdictSafe)
	v, _ = s.lookupJudged("ses-1", "perm-1")
	if v != verdictSafe {
		t.Errorf("re-record should overwrite; got %q, want safe", v)
	}

	// Empty permissionID is rejected silently (defensive; never happens
	// in practice but a buggy caller mustn't poison the cache key space).
	s.recordJudged("ses-1", "", verdictSafe)
	if _, ok := s.lookupJudged("ses-1", ""); ok {
		t.Errorf("empty permissionID should not be cached")
	}

	// nil-receiver short-circuit (defensive).
	var nilS *Service
	nilS.recordJudged("ses-1", "perm-1", verdictSafe)
	if _, ok := nilS.lookupJudged("ses-1", "perm-1"); ok {
		t.Errorf("nil-receiver lookup should miss")
	}
}

// TestEnsureAutoApproveSkipsAlreadyJudged is the regression for the
// reported bug: after the judge has already evaluated a permissionID,
// a subsequent Ensure call for the SAME (sessionID,
// permissionID) must short-circuit before claimAutoApprove — no second
// claim, no second goroutine, no second LLM call.
//
// This is exercised purely at the helper level (no real adapter) by
// verifying claimAutoApprove never grabs the slot when the cache
// already contains a verdict.
func TestEnsureAutoApproveSkipsAlreadyJudged(t *testing.T) {
	s := &Service{
		autoApprove: make(map[string]*autoApproveStatus),
		deps:        Deps{DefaultEnabled: true},
	}

	// Pre-seed the cache with an unsafe verdict (the canonical
	// reproduction case: judge said "unsafe", permission still pending,
	// user re-opens the session and REST polling re-fires
	// Ensure).
	s.recordJudged("ses-1", "perm-1", verdictUnsafe)

	// Call Ensure. It should short-circuit before claiming,
	// so no goroutine starts (cancel stays nil for the cached entry).
	s.Ensure("opencode", nil, "ses-1", "perm-1", "Bash command", nil, nil)

	if n := countInFlight(s); n != 0 {
		t.Errorf("cached permission should not claim a slot; got %d in-flight entries", n)
	}

	// A different permissionID for the same session must still run
	// (the cache is keyed on the exact OpenCode-generated ID).
	// We expect claimAutoApprove to succeed inside Ensure
	// and a goroutine to start; since we passed a nil adapter,
	// backgroundAutoApprove will warn-and-return immediately because
	// deps.SessionDir is nil in this test, but the claim
	// itself proves the short-circuit didn't fire.
	s.Ensure("opencode", nil, "ses-1", "perm-DIFFERENT", "Bash command", nil, nil)

	// Wait briefly for the goroutine to finish releasing its slot.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if countInFlight(s) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Now record the new permission as judged too, and verify the
	// short-circuit kicks in for it as well.
	s.recordJudged("ses-1", "perm-DIFFERENT", verdictSafe)
	s.Ensure("opencode", nil, "ses-1", "perm-DIFFERENT", "Bash command", nil, nil)
	if n := countInFlight(s); n != 0 {
		t.Errorf("cached safe verdict should also short-circuit; got %d in-flight entries", n)
	}
}

// countInFlight returns the number of status records currently
// representing a running judge goroutine (non-nil cancel). Replaces
// the pre-refactor `len(autoApproveInFlight)` reads scattered across
// these tests.
func countInFlight(s *Service) int {
	s.autoApproveMu.Lock()
	defer s.autoApproveMu.Unlock()
	n := 0
	for _, st := range s.autoApprove {
		if st != nil && st.cancel != nil {
			n++
		}
	}
	return n
}

// TestEnsureAutoApproveReplaysStateOnShortCircuit is the regression for
// the "frontend opens after the watcher already processed the
// permission" bug. The headless autoApproveWatcher claims permissions
// before any frontend tab is open. When the user later opens the
// session, the REST permission resurrection calls Ensure
// again, which previously just returned silently — leaving the
// frontend without ocman.permission.pending / .checking / .flagged /
// .auto-approved and therefore with no countdown and a stuck prompt.
//
// The fix: Ensure must replay the most recent applicable
// state event to the (potentially newly-registered) sink whenever it
// short-circuits.
func TestEnsureAutoApproveReplaysStateOnShortCircuit(t *testing.T) {
	tests := []struct {
		name          string
		setup         func(*Service, string, string) // pre-seed cache state
		wantEvent     string                         // SSE event type expected on the sink
		wantPayloadIn string                         // substring expected in the SSE payload
	}{
		{
			name: "in-flight: pending state replays as ocman.permission.pending",
			setup: func(s *Service, sid, pid string) {
				// Simulate the watcher having claimed but not yet finished.
				// judgeStartsAt in the near future so the reducer would
				// resume a live countdown.
				s.claimAutoApproveWithStart(context.Background(), sid, pid, time.Now().Add(2*time.Second).UnixMilli())
			},
			wantEvent:     "ocman.permission.pending",
			wantPayloadIn: `"judgeStartsAt":`,
		},
		{
			name: "in-flight + checking: replays as ocman.permission.checking",
			setup: func(s *Service, sid, pid string) {
				s.claimAutoApproveWithStart(context.Background(), sid, pid, time.Now().UnixMilli())
				s.markAutoApproveChecking(sid, pid)
			},
			wantEvent:     "ocman.permission.checking",
			wantPayloadIn: `"permissionId":"perm-replay"`,
		},
		{
			name: "judged unsafe with reasoning: replays as ocman.permission.flagged",
			setup: func(s *Service, sid, pid string) {
				s.recordJudgedWithReasoning(sid, pid, verdictUnsafe, "Writes to .env file.")
			},
			wantEvent:     "ocman.permission.flagged",
			wantPayloadIn: `"reasoning":"Writes to .env file."`,
		},
		{
			name: "judged safe: no replay (OpenCode cleared the prompt; DB has the notice)",
			setup: func(s *Service, sid, pid string) {
				s.recordJudgedWithReasoning(sid, pid, verdictSafe, "Read-only.")
			},
			wantEvent: "", // no event expected
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			s := &Service{
				sseSessions:  make(map[string]*Sink),
				autoApprove:  make(map[string]*autoApproveStatus),
				deps:         Deps{DefaultEnabled: true},
				judgeDelayMs: 0,
			}
			s.RegisterSink("ses-1", buf, nil)

			tc.setup(s, "ses-1", "perm-replay")

			// Call Ensure. It should short-circuit (cache
			// hit) and replay the current state to the sink instead of
			// starting a new judge goroutine.
			before := buf.Len()
			s.Ensure("opencode", nil, "ses-1", "perm-replay", "Bash command", nil, nil)
			got := buf.String()[before:]

			if tc.wantEvent == "" {
				if got != "" {
					t.Errorf("expected no event on safe-verdict short-circuit, got:\n%s", got)
				}
				return
			}
			wantHeader := "event: " + tc.wantEvent
			if !strings.Contains(got, wantHeader) {
				t.Errorf("expected %q in replay output, got:\n%s", wantHeader, got)
			}
			if tc.wantPayloadIn != "" && !strings.Contains(got, tc.wantPayloadIn) {
				t.Errorf("expected payload to contain %q, got:\n%s", tc.wantPayloadIn, got)
			}
		})
	}
}

// TestEnsureAutoApprove_BugRepro_FrontendConnectsAfterWatcherClaimed is
// the integration-flavoured reproduction of the reported bug:
//
//  1. Headless autoApproveWatcher observes permission.asked and claims
//     the slot before any frontend tab is open.
//  2. User then opens the session page. Frontend's REST /permissions
//     call lists the still-pending permission and fires
//     Ensure for it.
//  3. The frontend's SSE connection is open and its sink is registered.
//
// Before the fix, step 2 short-circuited silently and no
// ocman.permission.pending ever reached the sink — the countdown
// never started and the prompt UI stayed frozen. After the fix, step 2
// replays the cached pending state to the now-connected sink.
func TestEnsureAutoApprove_BugRepro_FrontendConnectsAfterWatcherClaimed(t *testing.T) {
	s := &Service{
		sseSessions:  make(map[string]*Sink),
		autoApprove:  make(map[string]*autoApproveStatus),
		deps:         Deps{DefaultEnabled: true},
		judgeDelayMs: 3000,
	}

	// 1. Simulate the watcher claiming first — frontend not yet open,
	//    no sink registered.
	originalAnchor := time.Now().Add(2 * time.Second).UnixMilli()
	_, ok := s.claimAutoApproveWithStart(context.Background(), "ses-1", "perm-1", originalAnchor)
	if !ok {
		t.Fatalf("simulated watcher claim should succeed")
	}

	// 2. Frontend opens session — sink registers, then REST resurrection
	//    fires Ensure for the still-pending permission.
	buf := &bytes.Buffer{}
	s.RegisterSink("ses-1", buf, nil)

	s.Ensure("opencode", nil, "ses-1", "perm-1", "Bash command", nil, nil)

	got := buf.String()
	if !strings.Contains(got, "event: ocman.permission.pending") {
		t.Fatalf("expected ocman.permission.pending on the sink after REST resurrection, got:\n%s", got)
	}
	// The replayed anchor must match the one the watcher recorded, so
	// the frontend countdown shows the remaining time rather than
	// restarting from zero. The exact value is what the watcher stored.
	wantAnchor := fmt.Sprintf(`"judgeStartsAt":%d`, originalAnchor)
	if !strings.Contains(got, wantAnchor) {
		t.Errorf("replay should preserve the original judgeStartsAt anchor (%d); got:\n%s", originalAnchor, got)
	}

	// And the replay must not have started a second goroutine.
	if n := countInFlight(s); n != 1 {
		t.Errorf("expected exactly 1 in-flight entry (the original watcher claim); got %d", n)
	}
}

// TestEnsureAutoApproveDoesNotStartSecondJudgeOnReplay verifies that
// the replay path never starts a new judge goroutine — replaying state
// for an already-judged permission must not race with the original
// judge or burn extra tokens.
func TestEnsureAutoApproveDoesNotStartSecondJudgeOnReplay(t *testing.T) {
	s := &Service{
		sseSessions: make(map[string]*Sink),
		autoApprove: make(map[string]*autoApproveStatus),
		deps:        Deps{DefaultEnabled: true},
	}
	s.recordJudgedWithReasoning("ses-1", "perm-1", verdictUnsafe, "Bad.")

	// In-flight slot must stay empty: replay path must not claim.
	s.Ensure("opencode", nil, "ses-1", "perm-1", "Bash command", nil, nil)

	s.autoApproveMu.Lock()
	defer s.autoApproveMu.Unlock()
	st := s.autoApprove["ses-1|perm-1"]
	if st == nil {
		t.Fatal("expected status to remain in cache after replay")
	}
	if st.cancel != nil {
		t.Errorf("replay must not create a new goroutine; cancel = %v", st.cancel)
	}
	if st.verdict != verdictUnsafe {
		t.Errorf("verdict should still be unsafe; got %q", st.verdict)
	}
}

// TestSseSinkRegistry verifies register / lookup / unregister semantics,
// including that unregister only removes the matching sink (a newer
// connection's registration must survive an older tear-down) and that
// writes against a closed sink are dropped instead of panicking.
func TestSseSinkRegistry(t *testing.T) {
	s := &Service{sseSessions: make(map[string]*Sink)}

	w1 := &bytes.Buffer{}
	w2 := &bytes.Buffer{}

	if got := s.lookupSink("ses-1"); got != nil {
		t.Fatalf("lookup before register should return nil")
	}

	sink1 := s.RegisterSink("ses-1", w1, nil)
	if got := s.lookupSink("ses-1"); got != sink1 {
		t.Errorf("after register, lookup should return sink1")
	}

	// Re-register: newer sink wins, previous one is closed.
	sink2 := s.RegisterSink("ses-1", w2, nil)
	if got := s.lookupSink("ses-1"); got != sink2 {
		t.Errorf("re-register should overwrite previous sink")
	}
	// Writes against the displaced sink should be no-ops.
	w1.Reset()
	sink1.write("ocman.permission.pending", []byte(`{}`))
	if w1.Len() != 0 {
		t.Errorf("displaced sink should not accept writes; got %q", w1.String())
	}

	// Older sink1 unregistering must not clear the entry for sink2.
	s.UnregisterSink("ses-1", sink1)
	if got := s.lookupSink("ses-1"); got != sink2 {
		t.Errorf("unregister with stale sink should NOT clear newer registration")
	}

	// Correct unregister clears and closes.
	s.UnregisterSink("ses-1", sink2)
	if got := s.lookupSink("ses-1"); got != nil {
		t.Errorf("unregister with matching sink should clear entry")
	}
	// Subsequent writes against the closed sink must be no-ops.
	w2.Reset()
	sink2.write("ocman.permission.pending", []byte(`{}`))
	if w2.Len() != 0 {
		t.Errorf("closed sink should not accept writes; got %q", w2.String())
	}
}

// TestSseSinkWriteAfterClose ensures writes never panic on a sink
// whose connection has been torn down. This is the exact scenario
// that crashed the running backend: the judge finishes long after
// the SSE connection closed and emits ocman.permission.checking.
func TestSseSinkWriteAfterClose(t *testing.T) {
	buf := &bytes.Buffer{}
	sink := &Sink{w: buf, flush: func() {}}

	sink.write("ocman.permission.pending", []byte(`{"a":1}`))
	if !strings.Contains(buf.String(), "event: ocman.permission.pending") {
		t.Errorf("write before close should reach the writer; got %q", buf.String())
	}

	sink.close()
	buf.Reset()
	sink.write("ocman.permission.checking", []byte(`{"b":2}`))
	if buf.Len() != 0 {
		t.Errorf("write after close should be a no-op; got %q", buf.String())
	}

	// close() is idempotent.
	sink.close()

	// write() on nil sink is a no-op (matches lookupSink miss path).
	var nilSink *Sink
	nilSink.write("ocman.permission.checking", []byte(`{"c":3}`))
}

// TestEmitPermissionPending writes through a connected sink and parses
// the resulting SSE bytes to verify the event shape.
func TestEmitPermissionPending(t *testing.T) {
	buf := &bytes.Buffer{}
	s := &Service{
		sseSessions: make(map[string]*Sink),
	}
	s.RegisterSink("ses-1", buf, nil)

	const wantJudgeStartsAt int64 = 1700000000123
	s.emitPermissionPending("ses-1", "perm-1", wantJudgeStartsAt)

	got := buf.String()
	if !strings.Contains(got, "event: ocman.permission.pending") {
		t.Errorf("missing event header in output:\n%s", got)
	}
	if !strings.Contains(got, `"permissionId":"perm-1"`) {
		t.Errorf("missing permissionId in payload:\n%s", got)
	}
	// sessionID (all caps) is the wire-format key: the frontend reducer's
	// eventSessionId() reads `sessionID` to route the event to the right
	// session's reducer. A regression to "sessionId" would route the
	// event into whichever session is currently viewed.
	if !strings.Contains(got, `"sessionID":"ses-1"`) {
		t.Errorf("missing sessionID (wire-format key) in payload:\n%s", got)
	}
	if strings.Contains(got, `"sessionId":`) {
		t.Errorf("payload must use sessionID (caps), not sessionId:\n%s", got)
	}
	// The judgeStartsAt anchor passed by the caller must round-trip
	// verbatim — replays rely on the same value being emitted twice so
	// the frontend reducer dedupes the second event.
	if !strings.Contains(got, `"judgeStartsAt":1700000000123`) {
		t.Errorf("expected judgeStartsAt to round-trip verbatim:\n%s", got)
	}

	// No sink registered → no-op (must not panic).
	s2 := &Service{sseSessions: make(map[string]*Sink)}
	s2.emitPermissionPending("missing", "perm-1", wantJudgeStartsAt)
}

func TestParseJudgeResponse(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantVerdict   judgeVerdict
		wantReasoning string
	}{
		// JSON happy path — verdict + reasoning both extracted.
		{
			"json safe",
			`{"verdict":"safe","reasoning":"Read-only operation.","risk_factors":[]}`,
			verdictSafe,
			"Read-only operation.",
		},
		{
			"json unsafe",
			`{"verdict":"unsafe","reasoning":"Writes to .env file.","risk_factors":[".env"]}`,
			verdictUnsafe,
			"Writes to .env file.",
		},
		{
			"json uppercase verdict",
			`{"verdict":"SAFE","reasoning":"OK"}`,
			verdictSafe,
			"OK",
		},
		{
			"json with leading text",
			"Here is the result:\n" + `{"verdict":"safe","reasoning":"Fine.","risk_factors":[]}`,
			verdictSafe,
			"Fine.",
		},
		{
			"json in markdown fences",
			"```json\n{\"verdict\":\"unsafe\",\"reasoning\":\"Dangerous.\"}\n```",
			verdictUnsafe,
			"Dangerous.",
		},
		{
			"json verdict only (no reasoning field)",
			`{"verdict":"safe"}`,
			verdictSafe,
			"",
		},
		{
			"reasoning whitespace is trimmed",
			`{"verdict":"safe","reasoning":"  spaced out.  "}`,
			verdictSafe,
			"spaced out.",
		},
		// Fallback keyword scan — verdict only, reasoning empty.
		{"bare SAFE", "SAFE", verdictSafe, ""},
		{"bare UNSAFE", "UNSAFE", verdictUnsafe, ""},
		{"lowercase safe fallback", "safe", verdictSafe, ""},
		{"lowercase unsafe fallback", "unsafe", verdictUnsafe, ""},
		{"SAFE with leading whitespace", "  SAFE  ", verdictSafe, ""},
		{"explanation with UNSAFE", "This is UNSAFE because it modifies files.", verdictUnsafe, ""},
		{"explanation with SAFE", "The action is SAFE — it only reads files.", verdictSafe, ""},
		{"empty string defaults unsafe", "", verdictUnsafe, ""},
		{"unrelated text defaults unsafe", "I cannot determine this.", verdictUnsafe, ""},
		// UNSAFE contains SAFE as a substring — must detect UNSAFE first.
		{"UNSAFE keyword scan", "UNSAFE", verdictUnsafe, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVerdict, gotReasoning := parseJudgeResponse(tt.input)
			if gotVerdict != tt.wantVerdict {
				t.Errorf("parseJudgeResponse(%q) verdict = %q, want %q", tt.input, gotVerdict, tt.wantVerdict)
			}
			if gotReasoning != tt.wantReasoning {
				t.Errorf("parseJudgeResponse(%q) reasoning = %q, want %q", tt.input, gotReasoning, tt.wantReasoning)
			}
		})
	}
}

func TestJudgePromptCustomSections(t *testing.T) {
	// No custom sections — output should not contain "##" beyond the built-in ones.
	base := judgePrompt("read file", []string{"*.go"}, nil, nil)
	if strings.Contains(base, "Feature branch") {
		t.Errorf("unexpected custom section in base prompt")
	}

	// One custom section.
	sections := []PromptSection{
		{Title: "Feature branch rule", Content: "git push to feature branches is SAFE."},
	}
	with := judgePrompt("git push", []string{"origin/feat"}, nil, sections)
	if !strings.Contains(with, "## Feature branch rule") {
		t.Errorf("custom section title not found in prompt")
	}
	if !strings.Contains(with, "git push to feature branches is SAFE.") {
		t.Errorf("custom section content not found in prompt")
	}

	// Empty title and content are skipped — no new sections added beyond base.
	base2 := judgePrompt("git push", nil, nil, nil)
	empty := judgePrompt("git push", nil, nil, []PromptSection{{Title: "", Content: ""}})
	if empty != base2 {
		t.Errorf("empty section should produce identical output to no sections")
	}

	// Blank title falls back to "Additional rule".
	noTitle := judgePrompt("git push", nil, nil, []PromptSection{{Title: "", Content: "some rule"}})
	if !strings.Contains(noTitle, "## Additional rule") {
		t.Errorf("blank title should fall back to 'Additional rule'")
	}

	disabled := false
	disabledRule := judgePrompt("git push", nil, nil, []PromptSection{{Title: "Disabled rule", Content: "UNIQUE_DISABLED_RULE_PAYLOAD", Enabled: &disabled}})
	if strings.Contains(disabledRule, "Disabled rule") || strings.Contains(disabledRule, "UNIQUE_DISABLED_RULE_PAYLOAD") {
		t.Errorf("disabled custom section should not be included in prompt")
	}
}

// TestJudgePromptDistinguishesCommands is the regression for the
// reported bug: "mkdir bla" and "rm bla" used to produce identical
// judge prompts because the permission text from OpenCode is the same
// generic label ("Bash command"). The metadata block must contain the
// actual command so the model can tell them apart.
func TestJudgePromptDistinguishesCommands(t *testing.T) {
	const permission = "Bash command" // OpenCode's generic label
	mkdir := judgePrompt(permission, nil, map[string]any{"command": "mkdir bla"}, nil)
	rm := judgePrompt(permission, nil, map[string]any{"command": "rm bla"}, nil)

	if mkdir == rm {
		t.Fatalf("prompt must differ between mkdir and rm; got identical prompts")
	}
	if !strings.Contains(mkdir, "command: \"mkdir bla\"") {
		t.Errorf("mkdir prompt missing the command metadata:\n%s", mkdir)
	}
	if !strings.Contains(rm, "command: \"rm bla\"") {
		t.Errorf("rm prompt missing the command metadata:\n%s", rm)
	}
	// Metadata block header must be present.
	if !strings.Contains(mkdir, "Tool input:") {
		t.Errorf("metadata block header missing:\n%s", mkdir)
	}
}

// TestFormatMetadataSection covers nil, empty, single-key, multi-key
// (sorted) and nested-value cases for the prompt metadata renderer.
func TestFormatMetadataSection(t *testing.T) {
	if got := formatMetadataSection(nil); got != "" {
		t.Errorf("nil metadata should produce empty section, got %q", got)
	}
	if got := formatMetadataSection(map[string]any{}); got != "" {
		t.Errorf("empty metadata should produce empty section, got %q", got)
	}

	single := formatMetadataSection(map[string]any{"command": "rm -rf /"})
	wantSingle := "Tool input:\n  command: \"rm -rf /\"\n"
	if single != wantSingle {
		t.Errorf("single key:\n  got:  %q\n  want: %q", single, wantSingle)
	}

	// Multi-key output is deterministic (sorted by key).
	multi := formatMetadataSection(map[string]any{
		"command":     "rm bla",
		"description": "delete file",
	})
	wantMulti := "Tool input:\n  command: \"rm bla\"\n  description: \"delete file\"\n"
	if multi != wantMulti {
		t.Errorf("multi key:\n  got:  %q\n  want: %q", multi, wantMulti)
	}

	// Nested values render verbatim (JSON-encoded).
	nested := formatMetadataSection(map[string]any{
		"args": []any{"-rf", "/tmp/x"},
	})
	if !strings.Contains(nested, `args: ["-rf","/tmp/x"]`) {
		t.Errorf("nested value not JSON-encoded:\n%s", nested)
	}
}

// TestSsePermissionTeeDispatch verifies that the tee correctly parses
// permission.asked events in both the OpenCode envelope shape
// ({type, properties}) and the legacy flat shape ({id, permission, ...}).
func TestSsePermissionTeeDispatch(t *testing.T) {
	tests := []struct {
		name          string
		sseData       string // raw SSE bytes written to the tee
		wantID        string
		wantSessionID string
		wantFired     bool
		wantMetadata  map[string]any // expected metadata propagated to onPermission
	}{
		{
			// OpenCode's actual on-the-wire shape: SSE default channel
			// (no "event:" line), type encoded inside the JSON envelope.
			// This is the regression that left the live auto-approve
			// path silently broken — dispatchEvent must derive the type
			// from envelope.Type when the SSE header is absent.
			name: "default channel (OpenCode current)",
			sseData: "data: " +
				`{"id":"evt_1","type":"permission.asked","properties":{"id":"perm-0","permission":"Bash command","patterns":[],"sessionID":"ses-1","metadata":{"command":"rm bla"}}}` +
				"\n\n",
			wantID:        "perm-0",
			wantSessionID: "ses-1",
			wantFired:     true,
			wantMetadata:  map[string]any{"command": "rm bla"},
		},
		{
			name: "named channel (event: header)",
			sseData: "event: permission.asked\ndata: " +
				`{"type":"permission.asked","properties":{"id":"perm-1","permission":"Read file","patterns":["*.txt"],"sessionID":"ses-1"}}` +
				"\n\n",
			wantID:        "perm-1",
			wantSessionID: "ses-1",
			wantFired:     true,
		},
		{
			name: "flat shape (legacy)",
			sseData: "event: permission.asked\ndata: " +
				`{"id":"perm-2","permission":"Write file","patterns":[],"sessionID":"ses-1","metadata":{"path":"/tmp/x"}}` +
				"\n\n",
			wantID:        "perm-2",
			wantSessionID: "ses-1",
			wantFired:     true,
			wantMetadata:  map[string]any{"path": "/tmp/x"},
		},
		{
			// Critical regression: OpenCode's /event stream is process-wide
			// so a tee on session-A's connection sees permission.asked for
			// session-B's prompt. onPermission must report session-B as the
			// event's sessionID — using session-A (the connection owner)
			// for routing would attribute the approval notice to the wrong
			// session.
			name: "cross-session event uses payload sessionID",
			sseData: "data: " +
				`{"id":"evt_3","type":"permission.asked","properties":{"id":"perm-cross","permission":"Bash command","patterns":[],"sessionID":"ses-B","metadata":{"command":"ls"}}}` +
				"\n\n",
			wantID:        "perm-cross",
			wantSessionID: "ses-B",
			wantFired:     true,
			wantMetadata:  map[string]any{"command": "ls"},
		},
		{
			name: "missing id — should not fire",
			sseData: "event: permission.asked\ndata: " +
				`{"type":"permission.asked","properties":{"permission":"Read file","sessionID":"ses-1"}}` +
				"\n\n",
			wantFired: false,
		},
		{
			name: "missing sessionID — should not fire",
			sseData: "event: permission.asked\ndata: " +
				`{"type":"permission.asked","properties":{"id":"perm-x","permission":"Read file"}}` +
				"\n\n",
			wantFired: false,
		},
		{
			name: "different event type (named channel) — should not fire",
			sseData: "event: message.created\ndata: " +
				`{"type":"message.created","properties":{"id":"msg-1"}}` +
				"\n\n",
			wantFired: false,
		},
		{
			name: "different event type (default channel) — should not fire",
			sseData: "data: " +
				`{"id":"evt_2","type":"message.created","properties":{"id":"msg-1"}}` +
				"\n\n",
			wantFired: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotID, gotSessionID string
			var gotMetadata map[string]any
			fired := make(chan struct{}, 1)

			tee := &Tee{
				W:     &bytes.Buffer{},
				Flush: nil,
				OnPermission: func(sessionID, permissionID, _ string, _ []string, metadata map[string]any) {
					gotSessionID = sessionID
					gotID = permissionID
					gotMetadata = metadata
					select {
					case fired <- struct{}{}:
					default:
					}
				},
			}

			// Write the SSE bytes via Write so drain runs inline (as it
			// would in production). onPermission is invoked inline from
			// dispatchEvent — callers are responsible for keeping it cheap.
			if _, err := tee.Write([]byte(tc.sseData)); err != nil {
				t.Fatalf("Write: %v", err)
			}

			if tc.wantFired {
				select {
				case <-fired:
				case <-time.After(time.Second):
					t.Fatalf("onPermission was not called within 1s")
				}
				if gotID != tc.wantID {
					t.Errorf("onPermission got permissionID=%q, want %q", gotID, tc.wantID)
				}
				if gotSessionID != tc.wantSessionID {
					t.Errorf("onPermission got sessionID=%q, want %q", gotSessionID, tc.wantSessionID)
				}
				if tc.wantMetadata != nil {
					if !reflect.DeepEqual(gotMetadata, tc.wantMetadata) {
						t.Errorf("onPermission got metadata=%v, want %v", gotMetadata, tc.wantMetadata)
					}
				}
			} else {
				select {
				case <-fired:
					t.Errorf("onPermission fired unexpectedly with %q", gotID)
				case <-time.After(50 * time.Millisecond):
				}
			}
		})
	}
}

// TestSsePermissionTeeRepliedDispatch verifies the tee fires
// onPermissionReplied for permission.replied events using the OpenCode
// `requestID` field as the permission ID and the payload's sessionID
// (NOT the connection's owning session) for routing.
func TestSsePermissionTeeRepliedDispatch(t *testing.T) {
	tests := []struct {
		name          string
		sseData       string
		wantID        string
		wantSessionID string
		wantFire      bool
	}{
		{
			name: "default channel (OpenCode current)",
			sseData: "data: " +
				`{"id":"evt_1","type":"permission.replied","properties":{"sessionID":"ses-1","requestID":"perm-1","reply":"once"}}` +
				"\n\n",
			wantID:        "perm-1",
			wantSessionID: "ses-1",
			wantFire:      true,
		},
		{
			name: "named channel",
			sseData: "event: permission.replied\ndata: " +
				`{"type":"permission.replied","properties":{"sessionID":"ses-1","requestID":"perm-2","reply":"reject"}}` +
				"\n\n",
			wantID:        "perm-2",
			wantSessionID: "ses-1",
			wantFire:      true,
		},
		{
			name: "cross-session reply uses payload sessionID",
			sseData: "data: " +
				`{"type":"permission.replied","properties":{"sessionID":"ses-B","requestID":"perm-cross","reply":"once"}}` +
				"\n\n",
			wantID:        "perm-cross",
			wantSessionID: "ses-B",
			wantFire:      true,
		},
		{
			name: "flat shape fallback (id instead of requestID)",
			sseData: "data: " +
				`{"type":"permission.replied","id":"perm-3","sessionID":"ses-1","reply":"always"}` +
				"\n\n",
			wantID:        "perm-3",
			wantSessionID: "ses-1",
			wantFire:      true,
		},
		{
			name: "missing requestID — should not fire",
			sseData: "data: " +
				`{"type":"permission.replied","properties":{"sessionID":"ses-1","reply":"once"}}` +
				"\n\n",
			wantFire: false,
		},
		{
			name: "missing sessionID — should not fire",
			sseData: "data: " +
				`{"type":"permission.replied","properties":{"requestID":"perm-x","reply":"once"}}` +
				"\n\n",
			wantFire: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotID, gotSessionID string
			fired := make(chan struct{}, 1)

			tee := &Tee{
				W:     &bytes.Buffer{},
				Flush: nil,
				OnPermissionReplied: func(sessionID, permissionID string) {
					gotSessionID = sessionID
					gotID = permissionID
					select {
					case fired <- struct{}{}:
					default:
					}
				},
			}
			if _, err := tee.Write([]byte(tc.sseData)); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if tc.wantFire {
				select {
				case <-fired:
				case <-time.After(time.Second):
					t.Fatalf("onPermissionReplied was not called within 1s")
				}
				if gotID != tc.wantID {
					t.Errorf("got permissionID=%q, want %q", gotID, tc.wantID)
				}
				if gotSessionID != tc.wantSessionID {
					t.Errorf("got sessionID=%q, want %q", gotSessionID, tc.wantSessionID)
				}
			} else {
				select {
				case <-fired:
					t.Errorf("onPermissionReplied fired unexpectedly with %q", gotID)
				case <-time.After(50 * time.Millisecond):
				}
			}
		})
	}
}

// TestEnsureAutoApproveCancellation verifies that Cancel
// interrupts the cancellable context observed by the goroutine. This
// is the contract that lets backgroundAutoApprove's ctx.Err() check
// short-circuit before RespondPermission is called.
func TestEnsureAutoApproveCancellation(t *testing.T) {
	s := &Service{autoApprove: make(map[string]*autoApproveStatus)}
	ctx, ok := s.claimAutoApprove(context.Background(), "ses-1", "perm-1")
	if !ok {
		t.Fatalf("claim failed")
	}
	// Run a goroutine that mimics backgroundAutoApprove's main wait
	// (e.g. the configured delay). Cancellation must unblock it
	// immediately.
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Errorf("ctx never cancelled")
		}
		close(done)
	}()

	if !s.Cancel("ses-1", "perm-1") {
		t.Fatalf("cancel returned false")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("goroutine did not unblock within 1s of cancel")
	}
	s.releaseAutoApprove("ses-1", "perm-1")
}

// TestCommandHash verifies the per-session safe-command cache key
// derivation. The hash must:
//   - return md5(metadata["command"]) as hex when a Bash command is
//     present (deterministic, identical inputs → identical hashes).
//   - return "" when metadata is nil, empty, missing the "command"
//     key, or the command is a non-string / empty string (no caching
//     for non-Bash tools or malformed payloads).
//   - differ for different command strings — even whitespace
//     variants produce distinct hashes, matching the user's
//     "exact command" requirement.
func TestCommandHash(t *testing.T) {
	// Same command → same hash.
	h1 := commandHash(map[string]any{"command": "ls -la"})
	h2 := commandHash(map[string]any{"command": "ls -la"})
	if h1 == "" {
		t.Fatalf("expected non-empty hash for valid Bash command")
	}
	if h1 != h2 {
		t.Errorf("identical commands should hash equal; got %q vs %q", h1, h2)
	}
	if len(h1) != 32 {
		t.Errorf("md5 hex should be 32 chars; got %d", len(h1))
	}

	// Different command → different hash.
	if commandHash(map[string]any{"command": "rm -rf /"}) == h1 {
		t.Errorf("different commands should hash differently")
	}

	// Whitespace difference → different hash (exact-command rule).
	if commandHash(map[string]any{"command": "ls  -la"}) == h1 {
		t.Errorf("whitespace-variant commands should hash differently (exact match required)")
	}

	// Missing key / nil / empty / wrong type → empty hash (no cache).
	cases := []map[string]any{
		nil,
		{},
		{"filePath": "/etc/passwd"}, // Edit tool, no "command"
		{"command": ""},             // empty command string
		{"command": 42},             // non-string command
		{"command": nil},            // nil command
	}
	for i, m := range cases {
		if got := commandHash(m); got != "" {
			t.Errorf("case %d: expected empty hash for non-Bash/invalid metadata, got %q (input=%v)", i, got, m)
		}
	}

	// Extra metadata keys are ignored — only "command" matters.
	withExtras := commandHash(map[string]any{
		"command":     "ls -la",
		"description": "list files",
		"cwd":         "/tmp",
	})
	if withExtras != h1 {
		t.Errorf("extra metadata keys should not affect hash; got %q vs %q", withExtras, h1)
	}
}

// TestSafeCommandCache verifies the per-session in-memory cache that
// short-circuits the LLM judge for identical Bash commands previously
// judged "safe" in the same session.
//
// Contract:
//   - lookup before record → miss.
//   - record then lookup → hit, returns the stored reasoning.
//   - cache is scoped per sessionID (same hash in different session → miss).
//   - empty hash is rejected (defensive — commandHash returns "" for
//     non-cacheable inputs, callers must not record empty keys).
//   - nil receiver is a no-op (defensive — matches the pattern used
//     by recordJudged / lookupJudged).
func TestSafeCommandCache(t *testing.T) {
	s := &Service{}

	hash := commandHash(map[string]any{"command": "git status"})
	if hash == "" {
		t.Fatalf("commandHash returned empty for valid command")
	}

	// Lookup before record → miss.
	if _, ok := s.lookupSafeCommandVerdict("ses-1", hash); ok {
		t.Errorf("lookup before record should miss")
	}

	// Record then lookup → hit, returns reasoning.
	s.recordSafeCommandVerdict("ses-1", hash, "Read-only git status.")
	reasoning, ok := s.lookupSafeCommandVerdict("ses-1", hash)
	if !ok {
		t.Fatalf("lookup after record should hit")
	}
	if reasoning != "Read-only git status." {
		t.Errorf("got reasoning=%q, want %q", reasoning, "Read-only git status.")
	}

	// Different session → miss (per-session scoping).
	if _, ok := s.lookupSafeCommandVerdict("ses-2", hash); ok {
		t.Errorf("same hash in different session should miss")
	}

	// Different command → miss (different hash).
	otherHash := commandHash(map[string]any{"command": "git diff"})
	if _, ok := s.lookupSafeCommandVerdict("ses-1", otherHash); ok {
		t.Errorf("different command should miss")
	}

	// Record on empty hash is a no-op (defensive: commandHash returns
	// "" for non-cacheable inputs).
	s.recordSafeCommandVerdict("ses-1", "", "should be ignored")
	if _, ok := s.lookupSafeCommandVerdict("ses-1", ""); ok {
		t.Errorf("empty hash should not be cached")
	}

	// Re-record overwrites.
	s.recordSafeCommandVerdict("ses-1", hash, "Updated reasoning.")
	reasoning, _ = s.lookupSafeCommandVerdict("ses-1", hash)
	if reasoning != "Updated reasoning." {
		t.Errorf("re-record should overwrite; got %q", reasoning)
	}

	// nil-receiver short-circuit (defensive).
	var nilS *Service
	nilS.recordSafeCommandVerdict("ses-1", hash, "x")
	if _, ok := nilS.lookupSafeCommandVerdict("ses-1", hash); ok {
		t.Errorf("nil-receiver lookup should miss")
	}
}

// TestInheritedSafeCommandVerdict covers a child session inheriting a
// parent's approved command via the ParentSessionID dep: an own-session
// hit returns unprefixed, an ancestor hit is prefixed with "inherited
// from parent: ", and unrelated / cyclic / resolver-less cases miss.
func TestInheritedSafeCommandVerdict(t *testing.T) {
	hash := commandHash(map[string]any{"command": "pnpm test"})

	// parent chain: grandchild -> child -> parent
	parents := map[string]string{
		"grandchild": "child",
		"child":      "parent",
	}
	s := &Service{deps: Deps{
		ParentSessionID: func(id string) (string, bool) {
			p, ok := parents[id]
			return p, ok
		},
	}}

	// Parent approved the command; child + grandchild have nothing.
	s.recordSafeCommandVerdict("parent", hash, "Read-only test run.")

	// Own-session hit → unprefixed.
	if r, ok := s.lookupInheritedSafeCommandVerdict("parent", hash); !ok || r != "Read-only test run." {
		t.Errorf("own-session hit: got (%q,%v), want (%q,true)", r, ok, "Read-only test run.")
	}

	// Child inherits parent → prefixed.
	want := "inherited from parent: Read-only test run."
	if r, ok := s.lookupInheritedSafeCommandVerdict("child", hash); !ok || r != want {
		t.Errorf("child inherit: got (%q,%v), want (%q,true)", r, ok, want)
	}

	// Grandchild walks two hops → prefixed.
	if r, ok := s.lookupInheritedSafeCommandVerdict("grandchild", hash); !ok || r != want {
		t.Errorf("grandchild inherit: got (%q,%v), want (%q,true)", r, ok, want)
	}

	// Unrelated session with no parent link → miss.
	if _, ok := s.lookupInheritedSafeCommandVerdict("orphan", hash); ok {
		t.Errorf("orphan session should miss")
	}

	// No resolver wired → falls back to own-session only.
	noResolver := &Service{}
	noResolver.recordSafeCommandVerdict("solo", hash, "x")
	if _, ok := noResolver.lookupInheritedSafeCommandVerdict("child", hash); ok {
		t.Errorf("no resolver: child must not inherit")
	}
	if _, ok := noResolver.lookupInheritedSafeCommandVerdict("solo", hash); !ok {
		t.Errorf("no resolver: own-session hit must still work")
	}

	// Cyclic parent link must not loop forever (maxParentWalk guard).
	cyclic := &Service{deps: Deps{
		ParentSessionID: func(id string) (string, bool) { return "a", true }, // a -> a is self; b -> a -> a...
	}}
	if _, ok := cyclic.lookupInheritedSafeCommandVerdict("b", hash); ok {
		t.Errorf("cyclic chain should miss, not loop")
	}
}

// TestBackgroundAutoApprove_SafeCommandCacheHit verifies the wire-in:
// when the per-session safe-command cache already has an entry for
// md5(metadata["command"]), backgroundAutoApprove must:
//
//  1. Skip the LLM judge entirely (proven by s.judge=nil: any judge
//     call would panic the test).
//  2. Skip the configured delay (cache hits are immediate; the delay
//     exists so the human can intervene before a *new* judgment runs).
//  3. Call adapter.RespondPermission with Reply="once" to clear the
//     pending prompt in OpenCode.
//  4. Record a judged verdict in the per-permissionID cache, prefixed
//     with "cached: " so the audit row makes the source clear.
//  5. Emit ocman.permission.auto-approved on the SSE sink so connected
//     clients render the approval notice without waiting for a reload.
func TestBackgroundAutoApprove_SafeCommandCacheHit(t *testing.T) {
	const (
		sessionID    = "ses-cache"
		permissionID = "perm-cache"
		command      = "pnpm test"
		permission   = "Bash command"
		origReason   = "Read-only test runner."
	)

	respondCh := make(chan platforms.RespondPermissionRequest, 1)
	fp := &fakePlatform{
		id: "opencode",
		respondPermissionFn: func(req platforms.RespondPermissionRequest) error {
			respondCh <- req
			return nil
		},
	}

	buf := &bytes.Buffer{}
	s := &Service{
		sseSessions:      make(map[string]*Sink),
		autoApprove:      make(map[string]*autoApproveStatus),
		deps:             Deps{DefaultEnabled: true},
		safeCommandCache: make(map[string]map[string]string),
		// judgeDelayMs deliberately large: a cache hit must bypass the
		// delay. If the implementation incorrectly waits, the test
		// will time out below.
		judgeDelayMs: 30000,
		// judge=nil: any attempt to consult the LLM panics, proving
		// the cache short-circuit fired.
	}
	s.RegisterSink(sessionID, buf, nil)

	// Seed the safe-command cache as if the judge had previously
	// approved this exact command in this session.
	hash := commandHash(map[string]any{"command": command})
	if hash == "" {
		t.Fatalf("test setup: commandHash returned empty")
	}
	s.recordSafeCommandVerdict(sessionID, hash, origReason)

	// Run backgroundAutoApprove directly with a fresh context. No
	// goroutine wrapping — we want to observe completion before
	// checking side effects.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.backgroundAutoApprove(
		ctx,
		platforms.ID("opencode"),
		fp,
		sessionID,
		permissionID,
		permission,
		nil,
		map[string]any{"command": command},
	)

	// 1+3. RespondPermission must have been called with Reply="once".
	select {
	case req := <-respondCh:
		if req.Reply != "once" {
			t.Errorf("RespondPermission reply = %q, want %q", req.Reply, "once")
		}
		if req.SessionID != sessionID || req.PermissionID != permissionID {
			t.Errorf("RespondPermission routing wrong: session=%q permission=%q", req.SessionID, req.PermissionID)
		}
	case <-time.After(time.Second):
		t.Fatalf("RespondPermission was not called — cache hit did not short-circuit")
	}

	// 4. recordJudgedWithReasoning must have stamped the per-permissionID
	//    cache so a later REST resurrection short-circuits without
	//    even checking the safe-command cache again.
	verdict, ok := s.lookupJudged(sessionID, permissionID)
	if !ok {
		t.Errorf("lookupJudged should hit after cache-driven approval")
	}
	if verdict != verdictSafe {
		t.Errorf("verdict = %q, want safe", verdict)
	}
	st, _ := s.lookupAutoApproveStatus(sessionID, permissionID)
	if !strings.HasPrefix(st.reasoning, "cached: ") {
		t.Errorf("stored reasoning should be prefixed with %q to mark cache origin; got %q", "cached: ", st.reasoning)
	}
	if !strings.Contains(st.reasoning, origReason) {
		t.Errorf("stored reasoning should preserve original judge reasoning %q; got %q", origReason, st.reasoning)
	}

	// 5. SSE sink must have received ocman.permission.auto-approved.
	got := buf.String()
	if !strings.Contains(got, "event: ocman.permission.auto-approved") {
		t.Errorf("expected ocman.permission.auto-approved on the sink, got:\n%s", got)
	}
}

// TestBackgroundAutoApprove_SafeCommandCacheMiss_DifferentSession is
// the per-session-scoping regression: caching `pnpm test` as safe in
// session A must NOT auto-approve the same raw command in session B.
// Cache scope is per-session by design.
//
// We exercise this by seeding session A's cache with a "safe" entry,
// then calling backgroundAutoApprove for session B. With s.judge=nil
// and no session-directory resolver, the function should fall through
// past the cache check (no hit) and attempt the normal judge path; the
// directory lookup fails because deps.SessionDir is nil, so the function
// warn-returns BEFORE ever touching the nil judge. Observable: no
// RespondPermission call.
func TestBackgroundAutoApprove_SafeCommandCacheMiss_DifferentSession(t *testing.T) {
	const command = "pnpm test"

	respondCalls := 0
	fp := &fakePlatform{
		id: "opencode",
		respondPermissionFn: func(req platforms.RespondPermissionRequest) error {
			respondCalls++
			return nil
		},
	}

	s := &Service{
		sseSessions:      make(map[string]*Sink),
		autoApprove:      make(map[string]*autoApproveStatus),
		deps:             Deps{DefaultEnabled: true},
		safeCommandCache: make(map[string]map[string]string),
		judgeDelayMs:     0,
	}

	// Cache hit exists for session A only.
	hash := commandHash(map[string]any{"command": command})
	s.recordSafeCommandVerdict("ses-A", hash, "Read-only.")

	// Run for session B — different sessionID, same hash.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	s.backgroundAutoApprove(
		ctx,
		platforms.ID("opencode"),
		fp,
		"ses-B",
		"perm-B",
		"Bash command",
		nil,
		map[string]any{"command": command},
	)

	if respondCalls != 0 {
		t.Errorf("session B should NOT have been auto-approved from session A's cache; RespondPermission called %d times", respondCalls)
	}
	// And no per-permissionID verdict should be recorded for the B
	// permission — the function bailed before reaching that path.
	if _, ok := s.lookupJudged("ses-B", "perm-B"); ok {
		t.Errorf("session B permission should not have a cached verdict")
	}
}

// TestSafeCommandCacheConcurrent stresses the cache under heavy parallel
// load to catch any locking bug. All goroutines write/read the same key;
// the final state must be consistent and no goroutine may panic.
func TestSafeCommandCacheConcurrent(t *testing.T) {
	s := &Service{}
	hash := commandHash(map[string]any{"command": "pnpm test"})
	if hash == "" {
		t.Fatalf("commandHash returned empty for valid command")
	}

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			s.recordSafeCommandVerdict("ses-x", hash, "Read-only test run.")
		}()
		go func() {
			defer wg.Done()
			_, _ = s.lookupSafeCommandVerdict("ses-x", hash)
		}()
	}
	wg.Wait()

	reasoning, ok := s.lookupSafeCommandVerdict("ses-x", hash)
	if !ok || reasoning != "Read-only test run." {
		t.Errorf("after concurrent writes/reads, lookup should hit; got (%q, %v)", reasoning, ok)
	}
}
