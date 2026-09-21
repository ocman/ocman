// Command ocman-plugin-slack connects a Slack thread to an ocman session over
// conversation.v1. Slack specifics stay here: Socket Mode, envelopes, scopes
// and tokens never cross the plugin protocol. ocman owns session creation.
//
// Slack app requirements (see docs/features/plugins.md):
//   - Socket Mode enabled, app-level token (xapp-) with connections:write.
//   - Bot token (xoxb-) with app_mentions:read and chat:write.
//   - Event subscription: app_mention only, so every inbound message is an
//     explicit @mention of the app.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	plugin "github.com/NoUseFreak/ocman/sdk/plugin"
)

const (
	defaultAPI      = "https://slack.com/api"
	reconnectDelay  = 3 * time.Second
	readLimitBytes  = 1 << 20
	eventBufferSize = 16
	// rateLimitRetries bounds in-call retries. A 429 is a rejection: Slack
	// posted nothing, so repeating it cannot duplicate a message.
	rateLimitRetries = 2
	// deliveredMemory bounds the per-operation outcomes kept in memory. It only
	// has to outlive the host's retry window for one reply.
	deliveredMemory = 1024
)

// maxRateLimitWait caps how long one reply call waits out Slack's Retry-After.
// Beyond it the host's own bounded backoff takes over, so a long cooldown never
// holds a plugin concurrency slot. A var so tests can shorten it.
var maxRateLimitWait = 20 * time.Second

type config struct {
	Project      string `json:"project"`
	AppToken     string `json:"appToken"`
	BotToken     string `json:"botToken"`
	AllowedUsers string `json:"allowedUsers"`
}

func description() plugin.Description {
	return plugin.Description{
		ID: "org.ocman.slack", Name: "Slack", Version: "1", Protocol: plugin.Version{Major: plugin.ProtocolMajor},
		Scope: plugin.ScopeOwner, MaxConcurrency: 4,
		Capabilities:    []plugin.Capability{plugin.ConversationCapability},
		RequestedGrants: []string{plugin.ConversationSessionGrant},
		Settings: []plugin.Setting{
			{Key: plugin.ConversationProjectSetting, Label: "Project directory", Type: "string", Required: true},
			{Key: "appToken", Label: "App-level token (xapp-)", Type: "string", Required: true, Secret: true},
			{Key: "botToken", Label: "Bot token (xoxb-)", Type: "string", Required: true, Secret: true},
			{Key: "allowedUsers", Label: "Authorized Slack user IDs (comma separated)", Type: "string", Required: true},
			{Key: "agent", Label: "Agent (optional)", Type: "string"},
			{Key: "model", Label: "Model (optional provider/model)", Type: "string"},
		},
	}
}

// readConfiguration consumes the single JSON line the host writes to fd 3
// before the serve hello. Secrets exist only in this process's memory.
func readConfiguration() (config, error) {
	f := os.NewFile(3, "config")
	if f == nil {
		return config{}, errors.New("missing configuration")
	}
	defer f.Close()
	line, err := bufio.NewReader(io.LimitReader(f, plugin.MaxMessageBytes+1)).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return config{}, err
	}
	var c config
	if err := json.Unmarshal(bytes.TrimSpace(line), &c); err != nil {
		return config{}, err
	}
	if c.Project == "" || c.AppToken == "" || c.BotToken == "" {
		return config{}, errors.New("incomplete configuration")
	}
	return c, nil
}

// outcome records what is known about one reply operation. A post whose
// outcome is unknown is deliberately not repeated: a duplicate message in a
// thread everyone can see is worse than a reply that may already be there.
type outcome int

const (
	outcomeUncertain outcome = iota + 1
	outcomePosted
)

type bot struct {
	cfg    config
	api    string
	client *http.Client

	mu       sync.Mutex
	outcomes map[string]outcome
	order    []string
}

func newBot(c config) *bot {
	b := &bot{
		cfg: c, api: defaultAPI, client: &http.Client{Timeout: 30 * time.Second},
		outcomes: make(map[string]outcome),
	}
	// ponytail: acceptance harness hook, not a user setting. ocman launches a
	// plugin with a fixed environment, so only a test wrapper can set this; it
	// points the two Web API calls at a mock workspace instead of Slack.
	if api := os.Getenv("OCMAN_SLACK_API"); api != "" {
		b.api = api
	}
	return b
}

// slackError distinguishes the two cases a retry has to tell apart: a request
// Slack rejected outright (nothing was posted, so repeating it is safe) and one
// whose outcome is unknown. Errors never carry the token.
type slackError struct {
	method     string
	reason     string
	uncertain  bool
	retryAfter time.Duration
}

func (e *slackError) Error() string {
	return fmt.Sprintf("slack %s failed: %s", e.method, e.reason)
}

// retryAfter reads Slack's cooldown, clamped so a hostile or absurd value
// cannot park the call. Slack always sends the header with a 429; a missing one
// falls back to the shortest documented window.
func retryAfter(res *http.Response) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(res.Header.Get("Retry-After")))
	if err != nil || seconds < 1 {
		seconds = 1
	}
	return min(time.Duration(seconds)*time.Second, maxRateLimitWait)
}

// call posts one Slack Web API method. Slack reports application failures in
// the body with HTTP 200, so ok is the real status; 429 and 5xx are reported by
// status code instead.
func (b *bot) call(ctx context.Context, method, token string, body any) (map[string]json.RawMessage, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &slackError{method: method, reason: "encoding request"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.api+"/"+method, bytes.NewReader(payload))
	if err != nil {
		return nil, &slackError{method: method, reason: "building request"}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	res, err := b.client.Do(req)
	if err != nil {
		// The request may have reached Slack before the transport failed.
		return nil, &slackError{method: method, reason: "transport", uncertain: true}
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusTooManyRequests:
		return nil, &slackError{method: method, reason: "rate_limited", retryAfter: retryAfter(res)}
	case res.StatusCode >= 500:
		return nil, &slackError{method: method, reason: "server error", uncertain: true}
	case res.StatusCode != http.StatusOK:
		return nil, &slackError{method: method, reason: "http " + strconv.Itoa(res.StatusCode)}
	}
	var decoded map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(res.Body, readLimitBytes)).Decode(&decoded); err != nil {
		// Slack answered and the answer was unreadable: the post may have run.
		return nil, &slackError{method: method, reason: "decoding response", uncertain: true}
	}
	var ok bool
	if json.Unmarshal(decoded["ok"], &ok) != nil || !ok {
		var reason string
		_ = json.Unmarshal(decoded["error"], &reason)
		return nil, &slackError{method: method, reason: reason}
	}
	return decoded, nil
}

func (b *bot) openSocket(ctx context.Context) (string, error) {
	decoded, err := b.call(ctx, "apps.connections.open", b.cfg.AppToken, struct{}{})
	if err != nil {
		return "", err
	}
	var url string
	if json.Unmarshal(decoded["url"], &url) != nil || url == "" {
		return "", errors.New("slack returned no socket url")
	}
	return url, nil
}

// record remembers one operation's outcome, evicting the oldest entry so the
// map stays bounded. A plugin restart forgets everything, which is why the
// host's guarantee is at-least-once rather than exactly-once.
func (b *bot) record(operationID string, o outcome) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.outcomes[operationID]; !exists {
		if len(b.order) >= deliveredMemory {
			delete(b.outcomes, b.order[0])
			b.order = b.order[1:]
		}
		b.order = append(b.order, operationID)
	}
	b.outcomes[operationID] = o
}

func (b *bot) knownOutcome(operationID string) outcome {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.outcomes[operationID]
}

// reply posts one completed assistant turn into the originating thread. The
// host retries an unacknowledged reply with the same operation id, so this is
// where a repeat is absorbed: a post already made, or one whose outcome is
// unknown, succeeds without posting again.
func (b *bot) reply(ctx context.Context, operationID string, r plugin.ConversationReply) error {
	channel, ts, ok := splitThread(r.ThreadID)
	if !ok {
		return plugin.Failure(plugin.ErrorInvalidArgument)
	}
	if known := b.knownOutcome(operationID); known != 0 {
		if known == outcomeUncertain {
			fmt.Fprintf(os.Stderr, "not repeating a reply whose outcome is unknown\n")
		}
		return nil
	}
	for attempt := 0; ; attempt++ {
		_, err := b.call(ctx, "chat.postMessage", b.cfg.BotToken, map[string]string{
			"channel": channel, "thread_ts": ts, "text": r.Text,
		})
		if err == nil {
			b.record(operationID, outcomePosted)
			return nil
		}
		fmt.Fprintln(os.Stderr, err)
		var failure *slackError
		if errors.As(err, &failure) && failure.uncertain {
			// Never repeated: the host acknowledges it on its next attempt.
			b.record(operationID, outcomeUncertain)
			return plugin.Failure(plugin.ErrorUnavailable)
		}
		// A rate-limited call posted nothing. Wait Slack's cooldown, bounded by
		// both the cap and the call deadline, then retry in place.
		if !errors.As(err, &failure) || failure.retryAfter == 0 || attempt >= rateLimitRetries || !sleep(ctx, failure.retryAfter) {
			return plugin.Failure(plugin.ErrorUnavailable)
		}
	}
}

// sleep waits out a provider cooldown, reporting whether it completed. A
// cancelled or expired context ends the attempt and leaves the retry to the
// host's own bounded backoff.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := description()
	var handler plugin.Handler
	var events chan plugin.Event
	if plugin.Mode(os.Args[1]) == plugin.ModeServe {
		cfg, err := readConfiguration()
		if err != nil {
			fmt.Fprintln(os.Stderr, "slack plugin configuration is incomplete")
			os.Exit(1)
		}
		b := newBot(cfg)
		events = make(chan plugin.Event, eventBufferSize)
		handler = plugin.ConversationHandler(b.reply)
		go b.listen(ctx, events)
	}
	if err := plugin.RunWithEvents(ctx, plugin.Mode(os.Args[1]), os.Getenv("OCMAN_PLUGIN_TOKEN"), d, os.Stdin, os.Stdout, handler, events); err != nil {
		os.Exit(1)
	}
}
