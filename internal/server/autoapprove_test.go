package server

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestClaimAutoApprove verifies the in-flight dedup behaviour: the
// first claim for a (session, permission) pair succeeds; subsequent
// claims return ok=false until releaseAutoApprove runs.
func TestClaimAutoApprove(t *testing.T) {
	s := &Server{autoApproveInFlight: make(map[string]context.CancelFunc)}

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
	s := &Server{autoApproveInFlight: make(map[string]context.CancelFunc)}
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

// TestCancelAutoApprove verifies cancelAutoApprove cancels the
// claimed context, returns true when something was cancelled and
// false otherwise, and that the entry survives until release (so
// double-cancel doesn't allow a new claimant to slip in).
func TestCancelAutoApprove(t *testing.T) {
	s := &Server{autoApproveInFlight: make(map[string]context.CancelFunc)}

	if s.cancelAutoApprove("ses-1", "perm-1") {
		t.Errorf("cancel with no claim should return false")
	}

	ctx, ok := s.claimAutoApprove(context.Background(), "ses-1", "perm-1")
	if !ok {
		t.Fatalf("claim failed")
	}
	if !s.cancelAutoApprove("ses-1", "perm-1") {
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
	s.cancelAutoApprove("ses-1", "perm-1")

	// Release frees the slot.
	s.releaseAutoApprove("ses-1", "perm-1")
	if _, ok := s.claimAutoApprove(context.Background(), "ses-1", "perm-1"); !ok {
		t.Errorf("re-claim after release should succeed")
	}
}

// TestJudgedPermissionsCache verifies that recordJudged / lookupJudged
// roundtrip correctly and that the key is (sessionID, permissionID).
func TestJudgedPermissionsCache(t *testing.T) {
	s := &Server{judgedPermissions: make(map[string]judgeVerdict)}

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
	var nilS *Server
	nilS.recordJudged("ses-1", "perm-1", verdictSafe)
	if _, ok := nilS.lookupJudged("ses-1", "perm-1"); ok {
		t.Errorf("nil-receiver lookup should miss")
	}
}

// TestEnsureAutoApproveSkipsAlreadyJudged is the regression for the
// reported bug: after the judge has already evaluated a permissionID,
// a subsequent ensureAutoApprove call for the SAME (sessionID,
// permissionID) must short-circuit before claimAutoApprove — no second
// claim, no second goroutine, no second LLM call.
//
// This is exercised purely at the helper level (no real adapter) by
// verifying claimAutoApprove never grabs the slot when the cache
// already contains a verdict.
func TestEnsureAutoApproveSkipsAlreadyJudged(t *testing.T) {
	s := &Server{
		autoApproveInFlight: make(map[string]context.CancelFunc),
		judgedPermissions:   make(map[string]judgeVerdict),
		autoApproveDefault:  true,
	}

	// Pre-seed the cache with an unsafe verdict (the canonical
	// reproduction case: judge said "unsafe", permission still pending,
	// user re-opens the session and REST polling re-fires
	// ensureAutoApprove).
	s.recordJudged("ses-1", "perm-1", verdictUnsafe)

	// Call ensureAutoApprove. It should short-circuit before even
	// trying to claim, so the in-flight map stays empty.
	s.ensureAutoApprove("opencode", nil, "ses-1", "perm-1", "Bash command", nil, nil)

	s.autoApproveInFlightMu.Lock()
	inflight := len(s.autoApproveInFlight)
	s.autoApproveInFlightMu.Unlock()
	if inflight != 0 {
		t.Errorf("cached permission should not claim a slot; got %d in-flight entries", inflight)
	}

	// A different permissionID for the same session must still run
	// (the cache is keyed on the exact OpenCode-generated ID).
	// We expect claimAutoApprove to succeed inside ensureAutoApprove
	// and a goroutine to start; since we passed a nil adapter,
	// backgroundAutoApprove will warn-and-return immediately when
	// dereferencing s.db (also nil in this test), but the claim
	// itself proves the short-circuit didn't fire.
	s.ensureAutoApprove("opencode", nil, "ses-1", "perm-DIFFERENT", "Bash command", nil, nil)

	// Wait briefly for the goroutine to finish releasing its slot.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		s.autoApproveInFlightMu.Lock()
		n := len(s.autoApproveInFlight)
		s.autoApproveInFlightMu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Now record the new permission as judged too, and verify the
	// short-circuit kicks in for it as well.
	s.recordJudged("ses-1", "perm-DIFFERENT", verdictSafe)
	s.ensureAutoApprove("opencode", nil, "ses-1", "perm-DIFFERENT", "Bash command", nil, nil)
	s.autoApproveInFlightMu.Lock()
	inflight2 := len(s.autoApproveInFlight)
	s.autoApproveInFlightMu.Unlock()
	if inflight2 != 0 {
		t.Errorf("cached safe verdict should also short-circuit; got %d in-flight entries", inflight2)
	}
}

// TestSseSinkRegistry verifies register / lookup / unregister semantics,
// including that unregister only removes the matching sink (a newer
// connection's registration must survive an older tear-down) and that
// writes against a closed sink are dropped instead of panicking.
func TestSseSinkRegistry(t *testing.T) {
	s := &Server{sseSessions: make(map[string]*sseSink)}

	w1 := &bytes.Buffer{}
	w2 := &bytes.Buffer{}

	if got := s.lookupSseSink("ses-1"); got != nil {
		t.Fatalf("lookup before register should return nil")
	}

	sink1 := s.registerSseSink("ses-1", w1, nil)
	if got := s.lookupSseSink("ses-1"); got != sink1 {
		t.Errorf("after register, lookup should return sink1")
	}

	// Re-register: newer sink wins, previous one is closed.
	sink2 := s.registerSseSink("ses-1", w2, nil)
	if got := s.lookupSseSink("ses-1"); got != sink2 {
		t.Errorf("re-register should overwrite previous sink")
	}
	// Writes against the displaced sink should be no-ops.
	w1.Reset()
	sink1.write("ocman.permission.pending", []byte(`{}`))
	if w1.Len() != 0 {
		t.Errorf("displaced sink should not accept writes; got %q", w1.String())
	}

	// Older sink1 unregistering must not clear the entry for sink2.
	s.unregisterSseSink("ses-1", sink1)
	if got := s.lookupSseSink("ses-1"); got != sink2 {
		t.Errorf("unregister with stale sink should NOT clear newer registration")
	}

	// Correct unregister clears and closes.
	s.unregisterSseSink("ses-1", sink2)
	if got := s.lookupSseSink("ses-1"); got != nil {
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
	sink := &sseSink{w: buf, flush: func() {}}

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

	// write() on nil sink is a no-op (matches lookupSseSink miss path).
	var nilSink *sseSink
	nilSink.write("ocman.permission.checking", []byte(`{"c":3}`))
}

// TestEmitPermissionPending writes through a connected sink and parses
// the resulting SSE bytes to verify the event shape.
func TestEmitPermissionPending(t *testing.T) {
	buf := &bytes.Buffer{}
	s := &Server{
		sseSessions:  make(map[string]*sseSink),
		judgeDelayMs: 3000,
	}
	s.registerSseSink("ses-1", buf, nil)

	s.emitPermissionPending("ses-1", "perm-1")

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
	if !strings.Contains(got, `"judgeStartsAt":`) {
		t.Errorf("missing judgeStartsAt in payload:\n%s", got)
	}

	// No sink registered → no-op (must not panic).
	s2 := &Server{sseSessions: make(map[string]*sseSink)}
	s2.emitPermissionPending("missing", "perm-1")
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

			tee := &ssePermissionTee{
				w:     &bytes.Buffer{},
				flush: nil,
				onPermission: func(sessionID, permissionID, _ string, _ []string, metadata map[string]any) {
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

			tee := &ssePermissionTee{
				w:     &bytes.Buffer{},
				flush: nil,
				onPermissionReplied: func(sessionID, permissionID string) {
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

// TestEnsureAutoApproveCancellation verifies that cancelAutoApprove
// interrupts the cancellable context observed by the goroutine. This
// is the contract that lets backgroundAutoApprove's ctx.Err() check
// short-circuit before RespondPermission is called.
func TestEnsureAutoApproveCancellation(t *testing.T) {
	s := &Server{autoApproveInFlight: make(map[string]context.CancelFunc)}
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

	if !s.cancelAutoApprove("ses-1", "perm-1") {
		t.Fatalf("cancel returned false")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("goroutine did not unblock within 1s of cancel")
	}
	s.releaseAutoApprove("ses-1", "perm-1")
}
