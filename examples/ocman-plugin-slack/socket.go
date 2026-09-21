package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	plugin "github.com/NoUseFreak/ocman/sdk/plugin"
)

type socketEnvelope struct {
	Type       string          `json:"type"`
	EnvelopeID string          `json:"envelope_id"`
	Payload    json.RawMessage `json:"payload"`
}

type eventPayload struct {
	EventID string `json:"event_id"`
	TeamID  string `json:"team_id"`
	Event   struct {
		Type        string          `json:"type"`
		Subtype     string          `json:"subtype"`
		User        string          `json:"user"`
		BotID       string          `json:"bot_id"`
		BotProfile  json.RawMessage `json:"bot_profile"`
		Text        string          `json:"text"`
		TS          string          `json:"ts"`
		ThreadTS    string          `json:"thread_ts"`
		Channel     string          `json:"channel"`
		ChannelType string          `json:"channel_type"`
	} `json:"event"`
}

var (
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
	return false
}

// translate accepts channel mentions and direct messages from authorized humans.
func (b *bot) translate(env socketEnvelope) (plugin.ConversationMessage, bool) {
	var payload eventPayload
	if env.Type != "events_api" || json.Unmarshal(env.Payload, &payload) != nil {
		return plugin.ConversationMessage{}, false
	}
	e := payload.Event
	acceptedType := e.Type == "app_mention" || e.Type == "message" && e.ChannelType == "im"
	if !acceptedType || e.Subtype != "" || e.BotID != "" || len(e.BotProfile) > 0 || !b.authorized(e.User) {
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
	message := plugin.ConversationMessage{
		AccountID: payload.TeamID, ThreadID: e.Channel + ":" + thread,
		EventID: payload.EventID, Text: strings.TrimSpace(leadingMention.ReplaceAllString(e.Text, "")), Project: b.cfg.Project,
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
	fmt.Fprintln(os.Stderr, "slack socket connected")
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
			fmt.Fprintln(os.Stderr, "ignored malformed slack socket frame")
			continue
		}
		if env.Type == "disconnect" {
			return nil
		}
		if env.EnvelopeID != "" {
			if err := conn.WriteJSON(map[string]string{"envelope_id": env.EnvelopeID}); err != nil {
				return errors.New("acknowledging slack envelope")
			}
		}
		if env.Type != "events_api" {
			continue
		}
		message, ok := b.translate(env)
		if !ok {
			fmt.Fprintln(os.Stderr, "ignored slack event")
			continue
		}
		event, err := plugin.NewConversationMessage(message)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ignored invalid slack mention")
			continue
		}
		fmt.Fprintln(os.Stderr, "forwarded slack mention")
		channel, thread, _ := splitThread(message.ThreadID)
		b.setThreadStatus(ctx, channel, thread, "Thinking...")
		select {
		case out <- event:
		case <-ctx.Done():
			return nil
		}
	}
}
