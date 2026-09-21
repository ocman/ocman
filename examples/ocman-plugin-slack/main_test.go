package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	plugin "github.com/NoUseFreak/ocman/sdk/plugin"
)

func testConfig() config {
	return config{Project: "/repo", AppToken: "xapp-test", BotToken: "xoxb-test", AllowedUsers: "U123, U456"}
}

func TestDescriptionSatisfiesContract(t *testing.T) {
	d := description()
	if err := d.Validate(); err != nil {
		t.Fatalf("description invalid: %v", err)
	}
	secrets := map[string]bool{}
	for _, s := range d.Settings {
		if s.Secret {
			secrets[s.Key] = true
		}
	}
	// Tokens must use the host's write-only secret handling.
	for _, key := range []string{"appToken", "botToken"} {
		if !secrets[key] {
			t.Fatalf("%s is not declared as a secret", key)
		}
	}
	if secrets[plugin.ConversationProjectSetting] {
		t.Fatal("the project must stay user-visible so it can be reviewed")
	}
}

// TestAPIBaseDefaultsToSlack pins the acceptance-harness hook: without the
// override every call goes to Slack itself.
func TestAPIBaseDefaultsToSlack(t *testing.T) {
	if b := newBot(testConfig()); b.api != defaultAPI {
		t.Fatalf("api %q", b.api)
	}
	t.Setenv("OCMAN_SLACK_API", "http://127.0.0.1:1/api")
	if b := newBot(testConfig()); b.api != "http://127.0.0.1:1/api" {
		t.Fatalf("api %q", b.api)
	}
}

func TestAuthorizedFailsClosed(t *testing.T) {
	for allowed, want := range map[string]bool{"U123": true, "U456": true, "U789": false, "": false} {
		b := &bot{cfg: testConfig()}
		if got := b.authorized(allowed); got != want {
			t.Fatalf("%q: %v", allowed, got)
		}
	}
	empty := &bot{cfg: config{}}
	if empty.authorized("U123") || empty.authorized("") {
		t.Fatal("an empty allow list must authorize nobody")
	}
}

func TestSplitThread(t *testing.T) {
	channel, ts, ok := splitThread("C123:1700000000.000100")
	if !ok || channel != "C123" || ts != "1700000000.000100" {
		t.Fatalf("%q %q %v", channel, ts, ok)
	}
	for _, bad := range []string{"C123", "C123:", ":1.0", "C 1:1.0", "C123:notats", "C123:1700000000.000100:x"} {
		if _, _, ok := splitThread(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
}

// mention wraps an event in a Socket Mode envelope. team_id and event_id sit on
// the payload, not the event: the envelope id is new on every delivery attempt,
// so only event_id is stable enough for the host to deduplicate on.
func mention(t *testing.T, event map[string]any) socketEnvelope {
	t.Helper()
	return mentionPayload(t, map[string]any{"team_id": "T1", "event_id": "Ev1", "event": event})
}

func mentionPayload(t *testing.T, payload map[string]any) socketEnvelope {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return socketEnvelope{Type: "events_api", EnvelopeID: "env-1", Payload: data}
}

func TestTranslate(t *testing.T) {
	b := &bot{cfg: testConfig()}
	base := map[string]any{"type": "app_mention", "user": "U123", "text": "<@U0BOT> ship it", "ts": "1700000000.000100", "channel": "C1"}
	message, ok := b.translate(mention(t, base))
	if !ok {
		t.Fatal("rejected an authorized mention")
	}
	if message.ThreadID != "C1:1700000000.000100" || message.Text != "ship it" || message.Project != "/repo" {
		t.Fatalf("%+v", message)
	}
	// The workspace and the stable event id must reach the host: they key the
	// durable mapping and the deduplication respectively.
	if message.AccountID != "T1" || message.EventID != "Ev1" {
		t.Fatalf("missing conversation identity: %+v", message)
	}
	// Mentions inside the request survive; only the addressing one is stripped.
	inner := map[string]any{}
	for k, v := range base {
		inner[k] = v
	}
	inner["text"] = "<@U0BOT> ask <@U456> about it"
	if message, ok = b.translate(mention(t, inner)); !ok || message.Text != "ask <@U456> about it" {
		t.Fatalf("%+v %v", message, ok)
	}

	// A reply inside a thread keeps the thread's root timestamp.
	threaded := map[string]any{}
	for k, v := range base {
		threaded[k] = v
	}
	threaded["thread_ts"] = "1699999999.000001"
	if message, ok = b.translate(mention(t, threaded)); !ok || message.ThreadID != "C1:1699999999.000001" {
		t.Fatalf("%+v %v", message, ok)
	}

	for name, mutate := range map[string]func(map[string]any){
		"unauthorized user": func(e map[string]any) { e["user"] = "U999" },
		"bot echo":          func(e map[string]any) { e["bot_id"] = "B1" },
		// A post from this very app carries a bot_profile rather than a
		// bot_id; accepting it would make every reply a new mention.
		"bot profile echo": func(e map[string]any) { e["bot_profile"] = map[string]any{"id": "B1"} },
		"message subtype":  func(e map[string]any) { e["subtype"] = "message_changed" },
		"not a mention":    func(e map[string]any) { e["type"] = "message" },
		"bad channel":      func(e map[string]any) { e["channel"] = "C 1" },
		"bad timestamp":    func(e map[string]any) { e["ts"] = "nope" },
		"mention only":     func(e map[string]any) { e["text"] = "<@U0BOT>" },
	} {
		t.Run(name, func(t *testing.T) {
			event := map[string]any{}
			for k, v := range base {
				event[k] = v
			}
			mutate(event)
			if _, ok := b.translate(mention(t, event)); ok {
				t.Fatal("accepted")
			}
		})
	}
	// A delivery the host cannot key or deduplicate is dropped, not guessed at.
	for name, payload := range map[string]map[string]any{
		"no team id":  {"event_id": "Ev1", "event": base},
		"no event id": {"team_id": "T1", "event": base},
		"bad team id": {"team_id": "T 1", "event_id": "Ev1", "event": base},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := b.translate(mentionPayload(t, payload)); ok {
				t.Fatal("accepted")
			}
		})
	}
	if _, ok := b.translate(socketEnvelope{Type: "hello"}); ok {
		t.Fatal("accepted a non-event frame")
	}
	if _, ok := b.translate(socketEnvelope{Type: "events_api", Payload: json.RawMessage(`not json`)}); ok {
		t.Fatal("accepted unparseable payload")
	}
}

// slackMock is a deterministic stand-in for Slack: the two Web API methods
// this plugin calls plus one Socket Mode connection.
type slackMock struct {
	*httptest.Server
	posts chan map[string]string
	acks  chan string
	send  chan any

	// Scripted failures, consumed one per chat.postMessage call: rateLimited
	// answers 429 with a cooldown (nothing posted), serverError answers 5xx
	// (the outcome is unknown to the caller).
	mu          sync.Mutex
	rateLimited int
	serverError int
}

// nextFailure consumes one scripted failure, if any.
func (m *slackMock) nextFailure() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.rateLimited > 0:
		m.rateLimited--
		return "rate-limited"
	case m.serverError > 0:
		m.serverError--
		return "server-error"
	}
	return ""
}

func newSlackMock(t *testing.T) *slackMock {
	t.Helper()
	m := &slackMock{posts: make(chan map[string]string, 4), acks: make(chan string, 4), send: make(chan any, 4)}
	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc("/apps.connections.open", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer xapp-test" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
			return
		}
		url := "ws" + strings.TrimPrefix(m.URL, "http") + "/link"
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "url": url})
	})
	mux.HandleFunc("/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch m.nextFailure() {
		case "rate-limited":
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		case "server-error":
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.Header.Get("Authorization") != "Bearer xoxb-test" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
			return
		}
		m.posts <- body
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/link", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for frame := range m.send {
			if conn.WriteJSON(frame) != nil {
				return
			}
			var ack map[string]string
			if conn.ReadJSON(&ack) != nil {
				return
			}
			m.acks <- ack["envelope_id"]
		}
	})
	m.Server = httptest.NewServer(mux)
	t.Cleanup(m.Close)
	return m
}

func (m *slackMock) bot() *bot {
	b := newBot(testConfig())
	b.api = m.URL
	return b
}

// TestSocketModeToNormalizedMessage proves the Slack half of the path against
// a mocked workspace: an authorized @mention becomes one normalized message,
// the envelope is acknowledged, and a completed reply lands in that thread.
func TestSocketModeToNormalizedMessage(t *testing.T) {
	m := newSlackMock(t)
	b := m.bot()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out := make(chan plugin.Event, 4)
	done := make(chan error, 1)
	go func() { done <- b.session(ctx, out) }()

	// An unauthorized mention is acknowledged but produces nothing.
	m.send <- mention(t, map[string]any{"type": "app_mention", "user": "U999", "text": "<@U0BOT> nope", "ts": "1700000000.000001", "channel": "C1"})
	if id := <-m.acks; id != "env-1" {
		t.Fatalf("ack %q", id)
	}
	m.send <- mention(t, map[string]any{"type": "app_mention", "user": "U123", "text": "<@U0BOT> ship it", "ts": "1700000000.000100", "channel": "C1"})
	<-m.acks

	var event plugin.Event
	select {
	case event = <-out:
	case <-ctx.Done():
		t.Fatal("no normalized message emitted")
	}
	if event.Capability != plugin.ConversationCapability.Name || event.Name != plugin.ConversationMessageEvent {
		t.Fatalf("event %+v", event)
	}
	var message plugin.ConversationMessage
	if json.Unmarshal(event.Data, &message) != nil || message.Text != "ship it" || message.ThreadID != "C1:1700000000.000100" {
		t.Fatalf("message %s", event.Data)
	}
	if len(out) != 0 {
		t.Fatal("unauthorized mention produced a message")
	}

	if err := b.reply(ctx, "conv-out:1", plugin.ConversationReply{AccountID: message.AccountID, ThreadID: message.ThreadID, Text: "shipped"}); err != nil {
		t.Fatal(err)
	}
	post := <-m.posts
	if post["channel"] != "C1" || post["thread_ts"] != "1700000000.000100" || post["text"] != "shipped" {
		t.Fatalf("post %+v", post)
	}

	m.send <- map[string]string{"type": "disconnect", "reason": "refresh_requested"}
	if err := <-done; err != nil {
		t.Fatalf("clean disconnect: %v", err)
	}
}

func TestReplyRejectsUnknownThread(t *testing.T) {
	m := newSlackMock(t)
	b := m.bot()
	if err := b.reply(t.Context(), "conv-out:1", plugin.ConversationReply{AccountID: "T1", ThreadID: "not-a-thread", Text: "x"}); err == nil {
		t.Fatal("accepted an unparseable thread")
	}
	b.cfg.BotToken = "wrong"
	if err := b.reply(t.Context(), "conv-out:2", plugin.ConversationReply{AccountID: "T1", ThreadID: "C1:1.0", Text: "x"}); err == nil {
		t.Fatal("a rejected post must fail the call")
	}
}

func slackReply(text string) plugin.ConversationReply {
	return plugin.ConversationReply{AccountID: "T1", ThreadID: "C1:1700000000.000100", Text: text}
}

// TestReplyWaitsOutRateLimit covers Slack's documented rate-limit response: the
// cooldown is respected in place, and because a 429 posted nothing, retrying it
// is the one repeat that cannot duplicate a message.
func TestReplyWaitsOutRateLimit(t *testing.T) {
	previous := maxRateLimitWait
	maxRateLimitWait = 20 * time.Millisecond
	t.Cleanup(func() { maxRateLimitWait = previous })

	m := newSlackMock(t)
	m.rateLimited = 2
	b := m.bot()
	start := time.Now()
	if err := b.reply(t.Context(), "conv-out:1", slackReply("shipped")); err != nil {
		t.Fatalf("a rate-limited reply must still be delivered: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 2*maxRateLimitWait {
		t.Fatalf("the cooldown was not respected: waited %s", elapsed)
	}
	if post := <-m.posts; post["text"] != "shipped" {
		t.Fatalf("post %+v", post)
	}
	if len(m.posts) != 0 {
		t.Fatal("a rate-limited reply posted more than once")
	}
	// Exhausting the in-call retries hands the delivery back to the host's own
	// bounded backoff instead of waiting indefinitely.
	m.rateLimited = rateLimitRetries + 1
	if err := b.reply(t.Context(), "conv-out:2", slackReply("later")); err == nil {
		t.Fatal("a persistent rate limit must fail the call, not loop")
	}
}

// TestReplyDoesNotRepeatUncertainPost is the "no blind repeat" rule: when the
// outcome of a post is unknown, the host's retry is absorbed rather than
// answered with a second visible message.
func TestReplyDoesNotRepeatUncertainPost(t *testing.T) {
	m := newSlackMock(t)
	m.serverError = 1
	b := m.bot()
	if err := b.reply(t.Context(), "conv-out:1", slackReply("shipped")); err == nil {
		t.Fatal("an unknown outcome must be reported to the host")
	}
	if err := b.reply(t.Context(), "conv-out:1", slackReply("shipped")); err != nil {
		t.Fatalf("the retry must be absorbed, not failed: %v", err)
	}
	if len(m.posts) != 0 {
		t.Fatal("a post with an unknown outcome was repeated")
	}
	// A different reply is unaffected: only the one uncertain operation is held.
	if err := b.reply(t.Context(), "conv-out:2", slackReply("next")); err != nil {
		t.Fatal(err)
	}
	if post := <-m.posts; post["text"] != "next" {
		t.Fatalf("post %+v", post)
	}
}

// TestReplyIgnoresRepeatOfDeliveredOperation covers the ordinary at-least-once
// case: the host lost the acknowledgment, not the post.
func TestReplyIgnoresRepeatOfDeliveredOperation(t *testing.T) {
	m := newSlackMock(t)
	b := m.bot()
	for range 2 {
		if err := b.reply(t.Context(), "conv-out:1", slackReply("shipped")); err != nil {
			t.Fatal(err)
		}
	}
	if post := <-m.posts; post["text"] != "shipped" {
		t.Fatalf("post %+v", post)
	}
	if len(m.posts) != 0 {
		t.Fatal("a redelivered reply posted twice")
	}
}

// TestRecordedOutcomesStayBounded proves the plugin's memory cannot grow with
// traffic; the trade-off is that a restart forgets and may repost.
func TestRecordedOutcomesStayBounded(t *testing.T) {
	b := newBot(testConfig())
	for i := range deliveredMemory + 10 {
		b.record("conv-out:"+strconv.Itoa(i), outcomePosted)
	}
	if len(b.outcomes) != deliveredMemory || len(b.order) != deliveredMemory {
		t.Fatalf("outcomes %d order %d", len(b.outcomes), len(b.order))
	}
	if b.knownOutcome("conv-out:0") != 0 {
		t.Fatal("the oldest outcome was not evicted")
	}
	if b.knownOutcome("conv-out:"+strconv.Itoa(deliveredMemory+9)) != outcomePosted {
		t.Fatal("the newest outcome was lost")
	}
	// Re-recording an existing operation must not consume a second slot.
	b.record("conv-out:"+strconv.Itoa(deliveredMemory+9), outcomeUncertain)
	if len(b.order) != deliveredMemory {
		t.Fatalf("order %d", len(b.order))
	}
}

func TestOpenSocketSurfacesSlackErrors(t *testing.T) {
	m := newSlackMock(t)
	b := m.bot()
	b.cfg.AppToken = "wrong"
	_, err := b.openSocket(t.Context())
	if err == nil || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("error must report the failure without the token: %v", err)
	}
}
