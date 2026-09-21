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
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	plugin "github.com/NoUseFreak/ocman/sdk/plugin"
)

const (
	defaultAPI      = "https://slack.com/api"
	reconnectDelay  = 3 * time.Second
	readLimitBytes  = 1 << 20
	eventBufferSize = 16
)

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

type bot struct {
	cfg    config
	api    string
	client *http.Client
}

func newBot(c config) *bot {
	return &bot{cfg: c, api: defaultAPI, client: &http.Client{Timeout: 30 * time.Second}}
}

// call posts one Slack Web API method. Slack reports failures in the body with
// HTTP 200, so ok is the real status. Errors never carry the token.
func (b *bot) call(ctx context.Context, method, token string, body any) (map[string]json.RawMessage, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.api+"/"+method, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	res, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s", method)
	}
	defer res.Body.Close()
	var decoded map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(res.Body, readLimitBytes)).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decoding %s", method)
	}
	var ok bool
	if json.Unmarshal(decoded["ok"], &ok) != nil || !ok {
		var reason string
		_ = json.Unmarshal(decoded["error"], &reason)
		return nil, fmt.Errorf("slack %s failed: %s", method, reason)
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

// reply posts one completed assistant turn into the originating thread.
func (b *bot) reply(ctx context.Context, r plugin.ConversationReply) error {
	channel, ts, ok := splitThread(r.ThreadID)
	if !ok {
		return plugin.Failure(plugin.ErrorInvalidArgument)
	}
	if _, err := b.call(ctx, "chat.postMessage", b.cfg.BotToken, map[string]string{
		"channel": channel, "thread_ts": ts, "text": r.Text,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return plugin.Failure(plugin.ErrorUnavailable)
	}
	return nil
}

type socketEnvelope struct {
	Type       string          `json:"type"`
	EnvelopeID string          `json:"envelope_id"`
	Payload    json.RawMessage `json:"payload"`
}

type eventPayload struct {
	// EventID is stable across Slack's retries of the same event, unlike the
	// Socket Mode envelope id, which is new on every delivery attempt. It is
	// what lets the host deduplicate a redelivery.
	EventID string `json:"event_id"`
	TeamID  string `json:"team_id"`
	Event   struct {
		Type       string          `json:"type"`
		Subtype    string          `json:"subtype"`
		User       string          `json:"user"`
		BotID      string          `json:"bot_id"`
		BotProfile json.RawMessage `json:"bot_profile"`
		Text       string          `json:"text"`
		TS         string          `json:"ts"`
		ThreadTS   string          `json:"thread_ts"`
		Channel    string          `json:"channel"`
	} `json:"event"`
}

var (
	// Only the addressing mention is stripped; mentions inside the request
	// itself are part of what the user asked for.
	leadingMention = regexp.MustCompile(`^(?:\s*<@[^>]*>)+\s*`)
	slackID        = regexp.MustCompile(`^[A-Za-z0-9]{1,32}$`)
	slackTS        = regexp.MustCompile(`^[0-9]{1,20}\.[0-9]{1,20}$`)
	slackEventID   = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
)

func splitThread(threadID string) (string, string, bool) {
	channel, ts, found := strings.Cut(threadID, ":")
	if !found || !slackID.MatchString(channel) || !slackTS.MatchString(ts) {
		return "", "", false
	}
	return channel, ts, true
}

func (b *bot) authorized(user string) bool {
	for _, allowed := range strings.Split(b.cfg.AllowedUsers, ",") {
		if trimmed := strings.TrimSpace(allowed); trimmed != "" && trimmed == user {
			return true
		}
	}
	// Fail closed: an empty or non-matching allow list authorizes nobody.
	return false
}

// translate converts a Socket Mode envelope into a normalized message. Only
// app_mention events from an authorized human are accepted, so every inbound
// message is an explicit @mention. Anything the app itself or another bot
// posted is dropped here, which is what keeps a reply from re-entering as a new
// mention and looping. Unknown frames are silently ignored.
func (b *bot) translate(env socketEnvelope) (plugin.ConversationMessage, bool) {
	var payload eventPayload
	if env.Type != "events_api" || json.Unmarshal(env.Payload, &payload) != nil {
		return plugin.ConversationMessage{}, false
	}
	e := payload.Event
	if e.Type != "app_mention" || e.Subtype != "" || e.BotID != "" || len(e.BotProfile) > 0 || !b.authorized(e.User) {
		return plugin.ConversationMessage{}, false
	}
	thread := e.ThreadTS
	if thread == "" {
		thread = e.TS
	}
	if !slackID.MatchString(e.Channel) || !slackTS.MatchString(thread) ||
		!slackID.MatchString(payload.TeamID) || !slackEventID.MatchString(payload.EventID) {
		return plugin.ConversationMessage{}, false
	}
	text := strings.TrimSpace(leadingMention.ReplaceAllString(e.Text, ""))
	message := plugin.ConversationMessage{
		AccountID: payload.TeamID, ThreadID: e.Channel + ":" + thread,
		EventID: payload.EventID, Text: text, Project: b.cfg.Project,
	}
	if message.Validate() != nil {
		return plugin.ConversationMessage{}, false
	}
	return message, true
}

// listen keeps one Socket Mode connection up, reconnecting with a fixed delay.
func (b *bot) listen(ctx context.Context, out chan<- plugin.Event) {
	for ctx.Err() == nil {
		if err := b.session(ctx, out); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(reconnectDelay):
		}
	}
}

func (b *bot) session(ctx context.Context, out chan<- plugin.Event) error {
	url, err := b.openSocket(ctx)
	if err != nil {
		return err
	}
	conn, handshake, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if handshake != nil {
		_ = handshake.Body.Close()
	}
	if err != nil {
		return errors.New("dialing slack socket mode")
	}
	defer conn.Close()
	conn.SetReadLimit(readLimitBytes)
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return errors.New("reading slack socket mode")
		}
		var env socketEnvelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		if env.Type == "disconnect" {
			return nil
		}
		if env.EnvelopeID != "" {
			// Acknowledge before doing any work; Slack retries unacked envelopes.
			if err := conn.WriteJSON(map[string]string{"envelope_id": env.EnvelopeID}); err != nil {
				return errors.New("acknowledging slack envelope")
			}
		}
		message, ok := b.translate(env)
		if !ok {
			continue
		}
		event, err := plugin.NewConversationMessage(message)
		if err != nil {
			continue
		}
		select {
		case out <- event:
		case <-ctx.Done():
			return nil
		}
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
