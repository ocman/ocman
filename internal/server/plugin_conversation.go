package server

import (
	"context"
	"encoding/json"
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
	"github.com/NoUseFreak/ocman/internal/worker"
)

// conversationFetchLimit bounds the messages read to extract one completed
// reply. Only the newest assistant message is used.
const conversationFetchLimit = 20

type conversationThread struct{ pluginID, threadID string }

// conversationSession identifies a session the way an idle edge does: a bare
// session id is not an identity across machines (see onSessionIdle).
type conversationSession struct {
	platformID, sessionID string
}

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
			// Reserved before dispatch, like actions: at-most-once matters more
			// than retrying, because a duplicate post is visible to everyone in
			// the thread. A dispatch that fails here loses that one reply.
			if err := s.stateDB.ReservePluginOperation(ctx, id, call.OperationID); err != nil {
				return nil, err
			}
			return p.Call(ctx, call)
		})
	// Serial per plugin (so one thread cannot fork two sessions) but unbounded,
	// so a slow turn start never starves the supervisor's event channel — an
	// unread event flood fails the plugin process.
	s.conversationJobs = worker.NewKeyed(s.runConversationJob, func(j conversationJob) string { return j.pluginID })
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
func (s *Server) startConversation(ctx context.Context, pluginID, threadID, dir, text string) error {
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) {
		return &plugins.WireError{Category: plugins.ErrorPermissionDenied}
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		// An unapproved or nonexistent project is a denial, not a crash.
		return &plugins.WireError{Category: plugins.ErrorPermissionDenied}
	}
	key := conversationThread{pluginID, threadID}
	// Safe check-then-act: the worker serializes every job for one plugin, so
	// two mentions in the same thread cannot race into two sessions.
	s.conversationMu.Lock()
	linked, resumed := s.conversationByThread[key]
	s.conversationMu.Unlock()
	if !resumed {
		host := s.router().ForDir(dir)
		ensured, err := host.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
		if err != nil {
			return err
		}
		platformID := "opencode"
		if id := host.RemoteID(); id != "" && id != "local" {
			platformID = remote.CompoundPlatformID(id, platformID)
		}
		created, err := s.sessions.Create(ctx, platformID, platforms.CreateSessionRequest{Directory: dir, Port: ensured.Port()})
		if err != nil {
			return err
		}
		linked = conversationSession{platformID: platformID, sessionID: created.ID}
		s.linkConversation(key, linked)
	}
	return s.sendNow(ctx, linked.platformID, platforms.SendMessageRequest{SessionID: linked.sessionID, Message: text})
}

// ponytail: thread links live for the host's lifetime. A restart also drops the
// idle edge that would deliver the reply, so persisting them buys nothing yet.
func (s *Server) linkConversation(key conversationThread, session conversationSession) {
	s.conversationMu.Lock()
	defer s.conversationMu.Unlock()
	if s.conversationByThread == nil {
		s.conversationByThread = map[conversationThread]conversationSession{}
		s.conversationBySession = map[conversationSession]conversationThread{}
	}
	if previous, ok := s.conversationByThread[key]; ok {
		delete(s.conversationBySession, previous)
	}
	s.conversationByThread[key] = session
	s.conversationBySession[session] = key
}

// replyToConversation posts the session's completed assistant turn back to the
// originating thread. Sessions with no linked thread are ignored.
func (s *Server) replyToConversation(ctx context.Context, platformID, sessionID string) {
	s.conversationMu.Lock()
	key, linked := s.conversationBySession[conversationSession{platformID: platformID, sessionID: sessionID}]
	s.conversationMu.Unlock()
	if !linked {
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
	if text == "" {
		return
	}
	// The message ID keys idempotency, so the same completed turn is posted once.
	operation := sessionID + ":" + messageID
	if err := s.conversations().Reply(ctx, key.pluginID, operation, plugins.ConversationReply{ThreadID: key.threadID, Text: text}); err != nil {
		log.WithError(err).WithFields(log.Fields{"plugin_id": key.pluginID, "session_id": sessionID}).
			Warn("posting completed reply to conversation thread")
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
