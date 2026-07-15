package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

// fakeSessionReader implements sessionReader for tests.
type fakeSessionReader struct {
	session  *db.Session
	messages []db.Message
	sessErr  error
	msgsErr  error
}

func (f *fakeSessionReader) GetSession(string) (*db.Session, error) {
	return f.session, f.sessErr
}

func (f *fakeSessionReader) GetSessionMessages(string) ([]db.Message, error) {
	return f.messages, f.msgsErr
}

// makeMsg builds a db.Message with the given role and text.
func makeMsg(role, text string) db.Message {
	data, _ := json.Marshal(map[string]string{"role": role, "text": text})
	return db.Message{ID: "m1", SessionID: "s1", Data: json.RawMessage(data)}
}

// noopGit is a gitRunner that always returns empty strings.
func noopGit(_ context.Context, _ string, _ ...string) (string, error) {
	return "", nil
}

// fakeGit returns a gitRunner that returns predefined values for
// specific subcommands.
func fakeGit(branch, diffStat string) gitRunner {
	return func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) >= 1 && args[0] == "branch" {
			return branch, nil
		}
		if len(args) >= 1 && args[0] == "diff" {
			return diffStat, nil
		}
		return "", nil
	}
}

func TestCompose_ContainsIntent(t *testing.T) {
	reader := &fakeSessionReader{
		session: &db.Session{ID: "s1", Directory: "/repo"},
	}
	c := NewPromptComposer(reader).withGitRunner(noopGit)

	prompt, err := c.Compose(context.Background(), "s1", "fix the linting issue", DefaultContextOptions())
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if !strings.Contains(prompt, "fix the linting issue") {
		t.Errorf("prompt does not contain intent: %q", prompt)
	}
	if !strings.Contains(prompt, "## Task") {
		t.Errorf("prompt missing ## Task section: %q", prompt)
	}
	if strings.Contains(prompt, "send_message_to_parent") {
		t.Errorf("prompt should not require an explicit report-back: %q", prompt)
	}
}

func TestCompose_ProjectMeta(t *testing.T) {
	reader := &fakeSessionReader{
		session: &db.Session{ID: "s1", Directory: "/home/user/repo", Title: "My Feature"},
	}
	c := NewPromptComposer(reader).withGitRunner(noopGit)

	prompt, _ := c.Compose(context.Background(), "s1", "intent", DefaultContextOptions())
	if !strings.Contains(prompt, "/home/user/repo") {
		t.Errorf("prompt missing directory: %q", prompt)
	}
	if !strings.Contains(prompt, "My Feature") {
		t.Errorf("prompt missing session title: %q", prompt)
	}
}

func TestCompose_GitBranchAndDiff(t *testing.T) {
	reader := &fakeSessionReader{
		session: &db.Session{ID: "s1", Directory: "/repo"},
	}
	c := NewPromptComposer(reader).withGitRunner(fakeGit("feature/my-branch", " main.go | 5 +++++"))

	prompt, _ := c.Compose(context.Background(), "s1", "intent", DefaultContextOptions())
	if !strings.Contains(prompt, "feature/my-branch") {
		t.Errorf("prompt missing branch: %q", prompt)
	}
	if !strings.Contains(prompt, "main.go") {
		t.Errorf("prompt missing diff stat: %q", prompt)
	}
}

func TestCompose_DirOverride(t *testing.T) {
	reader := &fakeSessionReader{
		session: &db.Session{ID: "s1", Directory: "/repo"},
	}
	override := "/repo/.worktrees/x"

	var gitDirs []string
	runner := func(_ context.Context, dir string, _ ...string) (string, error) {
		gitDirs = append(gitDirs, dir)
		return "", nil
	}
	c := NewPromptComposer(reader).withGitRunner(runner)

	opts := DefaultContextOptions()
	opts.DirOverride = override
	prompt, err := c.Compose(context.Background(), "s1", "intent", opts)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if !strings.Contains(prompt, override) {
		t.Errorf("prompt missing override dir: %q", prompt)
	}
	// The Directory line must reference the override, not the parent /repo.
	if strings.Contains(prompt, "**Directory**: `/repo`") {
		t.Errorf("prompt still references parent dir: %q", prompt)
	}
	for _, d := range gitDirs {
		if d != override {
			t.Errorf("git ran against %q, want override %q", d, override)
		}
	}
}

func TestCompose_RecentMessages(t *testing.T) {
	reader := &fakeSessionReader{
		session: &db.Session{ID: "s1", Directory: "/repo"},
		messages: []db.Message{
			makeMsg("user", "please fix the linting errors"),
			makeMsg("assistant", "I will fix the linting errors now"),
		},
	}
	c := NewPromptComposer(reader).withGitRunner(noopGit)

	prompt, _ := c.Compose(context.Background(), "s1", "intent", DefaultContextOptions())
	if !strings.Contains(prompt, "fix the linting errors") {
		t.Errorf("prompt missing recent messages: %q", prompt)
	}
	if !strings.Contains(prompt, "## Recent Conversation") {
		t.Errorf("prompt missing recent conversation section: %q", prompt)
	}
}

func TestCompose_DisabledContextOptions(t *testing.T) {
	reader := &fakeSessionReader{
		session: &db.Session{ID: "s1", Directory: "/repo", Title: "My Session"},
		messages: []db.Message{
			makeMsg("user", "some message"),
		},
	}
	c := NewPromptComposer(reader).withGitRunner(fakeGit("main", "file.go | 1 +"))

	opts := ContextOptions{
		RecentMessages: false,
		RelevantFiles:  false,
		GitBranch:      false,
		GitDiffStat:    false,
		ProjectMeta:    false,
		MaxChars:       defaultMaxPromptChars,
	}
	prompt, _ := c.Compose(context.Background(), "s1", "my intent", opts)

	if strings.Contains(prompt, "My Session") {
		t.Errorf("prompt should not contain project meta when disabled")
	}
	if strings.Contains(prompt, "main") {
		t.Errorf("prompt should not contain branch when disabled")
	}
	if strings.Contains(prompt, "file.go") {
		t.Errorf("prompt should not contain diff stat when disabled")
	}
	if strings.Contains(prompt, "some message") {
		t.Errorf("prompt should not contain recent messages when disabled")
	}
	// Intent is always present
	if !strings.Contains(prompt, "my intent") {
		t.Errorf("prompt must always contain intent")
	}
}

func TestCompose_CharacterCap(t *testing.T) {
	// Build a large recent messages section that exceeds the cap.
	longText := strings.Repeat("x", 200)
	var msgs []db.Message
	for i := 0; i < 20; i++ {
		msgs = append(msgs, makeMsg("user", longText))
	}
	reader := &fakeSessionReader{
		session:  &db.Session{ID: "s1", Directory: "/repo"},
		messages: msgs,
	}
	c := NewPromptComposer(reader).withGitRunner(noopGit)

	opts := DefaultContextOptions()
	opts.MaxChars = 500

	prompt, _ := c.Compose(context.Background(), "s1", "intent", opts)
	if len(prompt) > opts.MaxChars+10 { // small tolerance for trailing newline
		t.Errorf("prompt length %d exceeds cap %d", len(prompt), opts.MaxChars)
	}
}

func TestCompose_MissingSession(t *testing.T) {
	// When the session is not found, compose should still succeed with
	// a minimal prompt containing the intent.
	reader := &fakeSessionReader{
		session: nil,
		sessErr: errors.New("session not found"),
	}
	c := NewPromptComposer(reader).withGitRunner(noopGit)

	prompt, err := c.Compose(context.Background(), "s1", "my intent", DefaultContextOptions())
	if err != nil {
		t.Fatalf("Compose with missing session: %v", err)
	}
	if !strings.Contains(prompt, "my intent") {
		t.Errorf("prompt missing intent: %q", prompt)
	}
}

func TestExtractFilePaths(t *testing.T) {
	msgs := []db.Message{
		makeMsg("user", "please look at ./internal/server/tmux.go and /etc/hosts"),
		makeMsg("assistant", "I checked ./internal/server/tmux.go"),
	}
	paths := extractFilePaths(msgs)

	// Should deduplicate ./internal/server/tmux.go
	found := make(map[string]bool)
	for _, p := range paths {
		found[p] = true
	}
	if !found["./internal/server/tmux.go"] {
		t.Errorf("expected ./internal/server/tmux.go in paths, got %v", paths)
	}
	if !found["/etc/hosts"] {
		t.Errorf("expected /etc/hosts in paths, got %v", paths)
	}
	// Count occurrences of ./internal/server/tmux.go — should be 1 (deduplicated)
	count := 0
	for _, p := range paths {
		if p == "./internal/server/tmux.go" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 occurrence of ./internal/server/tmux.go, got %d", count)
	}
}

func TestExtractText_PartsFormat(t *testing.T) {
	data, _ := json.Marshal(map[string]interface{}{
		"role": "user",
		"parts": []map[string]string{
			{"type": "text", "text": "hello from parts"},
		},
	})
	text := extractText(json.RawMessage(data))
	if text != "hello from parts" {
		t.Errorf("expected 'hello from parts', got %q", text)
	}
}

func TestDefaultContextOptions(t *testing.T) {
	opts := DefaultContextOptions()
	if !opts.RecentMessages || !opts.RelevantFiles || !opts.GitBranch || !opts.GitDiffStat || !opts.ProjectMeta {
		t.Error("DefaultContextOptions should have all sources enabled")
	}
	if opts.MaxChars != defaultMaxPromptChars {
		t.Errorf("expected MaxChars=%d, got %d", defaultMaxPromptChars, opts.MaxChars)
	}
}

// TestParentModel covers parentModel's branches directly: nil composer,
// nil db, empty sessionID, DB error, unmarshalable/model-less rows, the
// nested "model" object, a bare model with no provider, and no-model tail.
func TestParentModel(t *testing.T) {
	msg := func(data string) db.Message { return db.Message{Data: json.RawMessage(data)} }
	tests := []struct {
		name string
		st   *splitTools
		sid  string
		want string
	}{
		{name: "nil composer", st: &splitTools{}, sid: "s1", want: ""},
		{name: "nil db", st: &splitTools{composer: &PromptComposer{}}, sid: "s1", want: ""},
		{name: "empty sessionID", st: &splitTools{composer: &PromptComposer{db: &fakeSessionReader{}}}, sid: "", want: ""},
		{name: "db error", st: &splitTools{composer: &PromptComposer{db: &fakeSessionReader{msgsErr: errors.New("boom")}}}, sid: "s1", want: ""},
		{
			name: "latest wins, skips unmarshalable and model-less rows",
			st: &splitTools{composer: &PromptComposer{db: &fakeSessionReader{messages: []db.Message{
				msg(`{"providerID":"openai","modelID":"gpt-5"}`),
				msg(`{"role":"user"}`),  // no model -> skip
				msg(`not json`),         // unmarshal error -> skip
				msg(`{"model":{"providerID":"anthropic","modelID":"claude"}}`), // nested wins (latest)
			}}}},
			sid:  "s1",
			want: "anthropic/claude",
		},
		{
			name: "bare model without provider",
			st:   &splitTools{composer: &PromptComposer{db: &fakeSessionReader{messages: []db.Message{msg(`{"modelID":"local-model"}`)}}}},
			sid:  "s1",
			want: "local-model",
		},
		{
			name: "no model-bearing message",
			st:   &splitTools{composer: &PromptComposer{db: &fakeSessionReader{messages: []db.Message{msg(`{"role":"user","text":"hi"}`)}}}},
			sid:  "s1",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.st.parentModel(tt.sid); got != tt.want {
				t.Fatalf("parentModel = %q, want %q", got, tt.want)
			}
		})
	}
}
