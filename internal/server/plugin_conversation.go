package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/worker"
)

// conversationFetchLimit bounds the messages read to extract one completed
// reply. Only the newest assistant message is used.
const conversationFetchLimit = 20

// conversationInboundPrefix namespaces inbound delivery receipts in the shared
// plugin operation table, so a message's event id can never collide with an
// outbound reply's key.
const conversationInboundPrefix = "conv-in:"

type conversationJob struct {
	ctx      context.Context
	pluginID string
	event    plugins.Event
}

// conversations is the grant-scoped seam between a conversation plugin and
// ocman's session orchestration. It is the only path a plugin has to a session.
func (s *Server) conversations() *plugins.ConversationBroker {
	s.conversationOnce.Do(s.buildConversations)
	return s.conversationBroker
}

func (s *Server) conversationWorker() *worker.Worker[conversationJob] {
	s.conversationOnce.Do(s.buildConversations)
	return s.conversationJobs
}

func (s *Server) buildConversations() {
	s.conversationBroker = plugins.NewConversationBroker(
		func(ctx context.Context, id string, use func(plugins.Description, []string, string) error) error {
			// Lock order matches actions: server lifecycle, then durable state.
			s.pluginMu.Lock()
			defer s.pluginMu.Unlock()
			if s.stateDB == nil {
				return plugins.ErrUnavailable
			}
			// No pluginScopeAllowed check: conversation.v1 has no remote
			// projection, so every delivery is already owner-local.
			return s.stateDB.WithPluginConversation(ctx, id, use)
		},
		s.startConversation,
		func(ctx context.Context, id string, call plugins.Call) (<-chan plugins.Reply, error) {
			// The authorization callback holds pluginMu through admission.
			p := s.pluginProcesses[id]
			if p == nil {
				return nil, plugins.ErrUnavailable
			}
			// No operation reservation here, unlike actions: the durable outbox
			// row is this call's receipt, and it has to survive being retried.
			// Reserving would turn the second attempt of an undelivered reply
			// into a permanent conflict.
			return p.Call(ctx, call)
		})
	// Serial per plugin (so one thread cannot fork two sessions) but unbounded,
	// so a slow turn start never starves the supervisor's event channel — an
	// unread event flood fails the plugin process.
	s.conversationJobs = worker.NewKeyed(s.runConversationJob, func(j conversationJob) string { return j.pluginID })
	s.conversationDeliveries = worker.NewKeyed(func(d state.PluginConversationDelivery) {
		runWithRecover("plugin-conversation-delivery", func() { s.deliverConversationReply(d) })
	}, state.PluginConversationDelivery.Group)
	s.conversationInFlight = make(map[int64]bool)
}

func (s *Server) runConversationJob(job conversationJob) {
	if err := s.conversations().Deliver(job.ctx, job.pluginID, job.event); err != nil {
		// Host-side categories and errors only; plugin payloads are never logged.
		log.WithError(err).WithFields(log.Fields{"plugin_id": job.pluginID, "capability": job.event.Capability, "event": job.event.Name}).
			Warn("delivering plugin conversation message")
	}
}

// consumePluginEvents drains one plugin's event channel until the process
// stops. Draining is mandatory: the supervisor fails a process whose events go
// unread. ctx is captured by the caller under pluginMu.
func (s *Server) consumePluginEvents(ctx context.Context, id string, p *plugins.Process) {
	for event := range p.Events() {
		s.conversationWorker().Enqueue(conversationJob{ctx: ctx, pluginID: id, event: event})
	}
}

// startConversation creates or resumes the thread's session in the plugin's one
// approved project and delivers the normalized text as a prompt. dir comes from
// the approved configuration; a plugin can never name another directory.
func (s *Server) startConversation(ctx context.Context, pluginID, dir string, message plugins.ConversationMessage) error {
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) {
		return &plugins.WireError{Category: plugins.ErrorPermissionDenied}
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		// An unapproved or nonexistent project is a denial, not a crash.
		return &plugins.WireError{Category: plugins.ErrorPermissionDenied}
	}
	if s.stateDB == nil {
		return plugins.ErrUnavailable
	}
	// Pressure is applied here, at the producer, and before the event receipt
	// is claimed: a full reply backlog pauses new integration work visibly
	// instead of dropping replies the host already owes. Not consuming the
	// receipt is what lets the provider's redelivery be accepted once the
	// backlog drains.
	if err := s.conversationBacklogPaused(ctx, pluginID); err != nil {
		return err
	}
	// Claim the delivery before doing any of its work. Reserve-then-act makes a
	// redelivered event at-most-once rather than at-least-once: a crash in the
	// window loses one prompt, where the other order would post a duplicate
	// prompt and a duplicate reply into a thread everyone can see. The receipt
	// is durable, so the dedup survives a restart of host and plugin alike.
	operation := conversationInboundPrefix + message.AccountID + ":" + message.EventID
	if err := s.stateDB.ReservePluginOperation(ctx, pluginID, operation); err != nil {
		var wire *plugins.WireError
		if errors.As(err, &wire) && wire.Category == plugins.ErrorConflict {
			// A duplicate provider delivery is expected traffic, not a failure.
			log.WithFields(log.Fields{"plugin_id": pluginID}).Debug("dropping duplicate conversation delivery")
			return nil
		}
		return err
	}
	key := state.PluginConversationKey{PluginID: pluginID, AccountID: message.AccountID, ThreadID: message.ThreadID}
	err := s.deliverConversationPrompt(ctx, key, dir, message)
	if err != nil {
		// The receipt above is already consumed, so no redelivery is coming for
		// this message: the thread would otherwise wait forever on an answer
		// that nobody is computing. Keyed on the event so a message that fails
		// twice cannot say so twice. Best-effort — the original failure is what
		// the caller acts on.
		s.appendConversationNotice(ctx, key,
			conversationNoticeOutcome+"unavailable:"+message.AccountID+":"+message.EventID,
			s.conversationNotice("unavailable", ""))
	}
	return err
}

// deliverConversationPrompt creates or resumes the thread's session and hands
// it the prompt. Split out from startConversation so every way of failing
// after the receipt was consumed reaches the same notice.
func (s *Server) deliverConversationPrompt(ctx context.Context, key state.PluginConversationKey, dir string, message plugins.ConversationMessage) error {
	linked, resumed, err := s.stateDB.GetPluginConversation(ctx, key)
	if err != nil {
		return err
	}
	if !resumed {
		if linked, err = s.createConversationSession(ctx, key, dir); err != nil {
			return err
		}
	}
	// Queue rather than send: an external message must never interleave into a
	// running turn the way a composer Enter deliberately does, because nobody in
	// the thread can see that a turn is in flight. forceQueue stays false so an
	// idle session still answers immediately; the queue's own busy gate holds it
	// for the next session.idle edge otherwise, preserving arrival order. A
	// mapped session that can no longer be sent to is set aside by the queue
	// after repeated failures, with a queue.updated broadcast.
	return s.queueSvc().Enqueue(ctx, linked.PlatformID,
		false, platforms.SendMessageRequest{SessionID: linked.SessionID, Message: message.Text})
}

// createConversationSession launches the project's instance, creates the
// session and claims the mapping. Losing the claim is not an error: the winner's
// session is authoritative and this one is abandoned, so a concurrent first
// message can never leave the conversation with two mapped sessions.
func (s *Server) createConversationSession(ctx context.Context, key state.PluginConversationKey, dir string) (state.PluginConversationSession, error) {
	host := s.router().ForDir(dir)
	ensured, err := host.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
	if err != nil {
		return state.PluginConversationSession{}, err
	}
	platformID := "opencode"
	if id := host.RemoteID(); id != "" && id != "local" {
		platformID = remote.CompoundPlatformID(id, platformID)
	}
	created, err := s.sessions.Create(ctx, platformID, platforms.CreateSessionRequest{Directory: dir, Port: ensured.Port()})
	if err != nil {
		return state.PluginConversationSession{}, err
	}
	linked, won, err := s.stateDB.LinkPluginConversation(ctx, key,
		state.PluginConversationSession{PlatformID: platformID, SessionID: created.ID})
	if err != nil {
		return state.PluginConversationSession{}, err
	}
	if !won {
		log.WithFields(log.Fields{"plugin_id": key.PluginID, "session_id": created.ID, "mapped_session_id": linked.SessionID}).
			Warn("abandoning conversation session that lost the mapping claim")
	}
	return linked, nil
}

// replyToConversation records the session's completed assistant turn for
// delivery to the originating thread. Sessions with no linked thread are
// ignored. The mapping is read from the database, so a turn that completes
// after a restart still finds its thread.
//
// This function never talks to the provider. It appends the reply to the
// durable outbox and wakes the delivery pump: a disconnect, a plugin crash or a
// host restart between here and the post replays the delivery instead of losing
// a completed reply.
func (s *Server) replyToConversation(ctx context.Context, platformID, sessionID string) {
	if s.stateDB == nil {
		return
	}
	key, linked, err := s.stateDB.GetPluginConversationThread(ctx,
		state.PluginConversationSession{PlatformID: platformID, SessionID: sessionID})
	if err != nil || !linked {
		return
	}
	adapter, ok := s.adapterForSession(ctx, platformID, sessionID)
	if !ok {
		return
	}
	detail, err := adapter.Session(ctx, sessionID, conversationFetchLimit, 0)
	if err != nil || detail == nil {
		log.WithError(err).WithField("session_id", sessionID).Warn("reading completed turn for conversation reply")
		return
	}
	messageID, text := latestAssistantText(detail.Messages, detail.Parts)
	// A failed turn produces no answer, or a partial one, so the thread needs
	// telling that this is where it stopped. Keyed on the message the error is
	// recorded against — an error always lands on the last assistant message —
	// so a repeated idle edge for the same failed turn reports once, while the
	// next turn's failure is a new notice.
	if messageID != "" && detail.Session != nil && detail.Session.Status == db.StatusError {
		s.appendConversationNotice(ctx, key, sessionID+conversationNoticeOutcome+"error:"+messageID,
			s.conversationNotice("error", sessionID))
	}
	if text == "" {
		return
	}
	// The message ID keys the append, so a repeated idle edge for the same
	// completed turn adds no second delivery. The receipt outlives the
	// delivery, so this holds after the reply has been acknowledged.
	delivery, err := s.stateDB.AppendPluginConversationReply(ctx, key, sessionID+":"+messageID, text)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{"plugin_id": key.PluginID, "session_id": sessionID}).
			Error("recording completed reply for delivery")
		return
	}
	if delivery != 0 {
		s.kickConversationOutbox()
	}
}

// latestAssistantText returns the newest assistant message's ID and its
// concatenated text parts. Reasoning, tool and file parts are dropped: v1
// replies are the plain assistant answer.
func latestAssistantText(messages []db.Message, parts []db.Part) (string, string) {
	var latest db.Message
	for _, message := range messages {
		var data db.MessageData
		if json.Unmarshal(message.Data, &data) != nil || data.Role != "assistant" {
			continue
		}
		if latest.ID == "" || message.TimeCreated > latest.TimeCreated ||
			(message.TimeCreated == latest.TimeCreated && message.ID > latest.ID) {
			latest = message
		}
	}
	if latest.ID == "" {
		return "", ""
	}
	selected := make([]db.Part, 0, len(parts))
	for _, part := range parts {
		if part.MessageID == latest.ID {
			selected = append(selected, part)
		}
	}
	// Stable, with no ID tiebreaker: the live adapter leaves TimeCreated zero,
	// so equal timestamps must keep the adapter's arrival order.
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].TimeCreated < selected[j].TimeCreated })
	var text []string
	for _, part := range selected {
		var payload struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(part.Data, &payload) == nil && payload.Type == "text" && strings.TrimSpace(payload.Text) != "" {
			text = append(text, strings.TrimRight(payload.Text, "\n"))
		}
	}
	return latest.ID, conversationReplyText(strings.Join(text, "\n\n"))
}

// conversationReplyText makes host-authored assistant output satisfy the
// capability's text rules. Assistant answers can contain terminal escapes and
// other control characters; dropping the whole reply over one would be worse.
func conversationReplyText(text string) string {
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			return r
		}
		return -1
	}, strings.ToValidUTF8(text, ""))
	text = strings.TrimSpace(text)
	if len(text) > plugins.ConversationMaxTextBytes {
		text = strings.ToValidUTF8(text[:plugins.ConversationMaxTextBytes], "")
	}
	return text
}
