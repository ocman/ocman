package pluginsmod

import (
	"context"
	"net/url"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/state"
)

// notice.go tells a conversation thread that its session
// cannot proceed on its own, or did not end in an answer. A thread only ever
// sees the assistant's completed replies, so without these a session blocked on
// a permission prompt, or one whose turn errored, looks to everyone in the
// thread exactly like a session that is still thinking.
//
// Three rules shape every notice:
//
//   - Notices carry no session content. The text is one of the fixed sentences
//     below plus a link — never the permission text, the patterns, the tool
//     metadata, the error message or any part of the transcript. A thread is
//     usually a wider audience than the session's own operator, so the safe
//     default is that the notice says only that attention is needed and where
//     to give it.
//   - Notices do not accept decisions. The link is the whole answer: the
//     permission is answered in ocman, under ocman's existing provenance and
//     auto-approval rules. Nothing here approves, denies or pre-empts a prompt,
//     so a thread can never become a second approval channel.
//   - Notices resolve rather than retract. Nothing is ever unsent — a posted
//     message in a provider thread is public, and editing it away would be a
//     second surprise. A notice is resolved by what follows it in the same
//     thread: answer the prompt in ocman and the turn continues, so the
//     completed reply lands under the notice. Re-observing the same prompt (a
//     stream reconnect, a replayed notification, an ocman restart) appends
//     nothing, because the outbox key below is the prompt's own request ID.
const (
	// conversationNoticeAttention keys a prompt that is waiting on the user.
	// The request ID makes it stable: one notice per prompt, forever.
	conversationNoticeAttention = ":attention:"
	// conversationNoticeOutcome keys a turn that ended somewhere other than a
	// completed answer.
	conversationNoticeOutcome = ":outcome:"
)

// conversationNoticeTimeout bounds a notice's own database work. A notice is
// an aside to whatever asked for it; it must never hold up a prompt or an SSE
// stream.
const conversationNoticeTimeout = 5 * time.Second

// conversationNoticeText maps a notice kind to its fixed sentence. Keeping
// every sentence in one table is what makes "no session content leaks" a
// property you can check by reading rather than by tracing call sites.
var conversationNoticeText = map[string]string{
	"permission":  "This session needs a permission decision in ocman before it can continue.",
	"question":    "This session is waiting on an answer in ocman before it can continue.",
	"error":       "This session's last turn ended with an error. Open it in ocman for the details.",
	"unavailable": "Your message could not be delivered to a session, so nothing is running for it. Open ocman to retry.",
}

// sessionURL builds a link a reader elsewhere can actually open. Without a
// configured public base URL this degrades to the loopback address, which is
// right for a local reader and useless to a remote one — see
// docs/features/plugins.md, which tells operators to set OCMAN_PUBLIC_BASE_URL
// before pointing a conversation plugin at a remote provider.
func (m *Module) sessionURL(sessionID string) string {
	if sessionID == "" {
		return m.deps.PublicURL("/")
	}
	return m.deps.PublicURL("/session/" + url.PathEscape(sessionID))
}

// conversationNotice renders one notice. Unknown kinds render nothing rather
// than an empty-bodied link.
func (m *Module) conversationNotice(kind, sessionID string) string {
	sentence, ok := conversationNoticeText[kind]
	if !ok {
		return ""
	}
	// Through the same sanitiser as an assistant reply: the link is built from
	// a session ID, and the capability's text rules apply to host-authored
	// text exactly as they do to an answer.
	return conversationReplyText(sentence + "\n" + m.sessionURL(sessionID))
}

func (m *Module) conversationNoticeFor(ctx context.Context, kind, platformID, sessionID string) string {
	if m.deps.SessionURL == nil || sessionID == "" {
		return m.conversationNotice(kind, sessionID)
	}
	sentence, ok := conversationNoticeText[kind]
	if !ok {
		return ""
	}
	link, err := m.deps.SessionURL(ctx, platformID, sessionID)
	if err != nil {
		// A missing binding is not permission to publish a guessed native link.
		// Keep the notice actionable at the app root without leaking content.
		log.WithError(err).Warn("resolving conversation notice session link")
		link = m.deps.PublicURL("/")
	}
	return conversationReplyText(sentence + "\n" + link)
}

// appendConversationNotice records a notice for delivery to one thread.
// operation is the dedup key: appending the same operation twice adds one
// delivery, which is what makes a re-observed prompt silent.
func (m *Module) appendConversationNotice(ctx context.Context, key state.PluginConversationKey, operation, text string) {
	if m.store == nil || text == "" {
		return
	}
	// Detached from the caller's context on purpose. A notice usually reports
	// the very failure that exhausted that context — an agent that could not be
	// reached before the deadline — and inheriting it would silently drop the
	// notice in exactly the case it exists for.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), conversationNoticeTimeout)
	defer cancel()
	delivery, err := m.store.AppendPluginConversationReply(ctx, key, operation, text)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{"plugin_id": key.PluginID}).
			Error("recording conversation notice for delivery")
		return
	}
	if delivery != 0 {
		m.kickConversationOutbox()
	}
}

// notifyConversationSession resolves a session's thread and posts a notice into
// it. A session with no thread — which is nearly all of them — costs one
// indexed lookup and produces nothing.
func (m *Module) notifyConversationSession(ctx context.Context, platformID, sessionID, operation, kind string) {
	if m.store == nil || sessionID == "" {
		return
	}
	key, linked, err := m.store.GetPluginConversationThread(ctx,
		state.PluginConversationSession{PlatformID: platformID, SessionID: sessionID})
	if err != nil || !linked {
		return
	}
	m.appendConversationNotice(ctx, key, operation, m.conversationNoticeFor(ctx, kind, platformID, sessionID))
}

// PromptNeedsUser is the autoapprove pipeline's PromptNeedsUser
// hook. It fires once the pipeline has decided a prompt is the user's to
// answer, never on the raw ask, so a permission the judge approves produces no
// notice at all.
func (m *Module) PromptNeedsUser(platformID, sessionID, kind, requestID string) {
	if m.store == nil || requestID == "" {
		return
	}
	if _, ok := conversationNoticeText[kind]; !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), conversationNoticeTimeout)
	defer cancel()
	prefix, err := m.pluginWireSession(ctx, platformID, sessionID)
	if err != nil {
		return
	}
	m.notifyConversationSession(ctx, platformID, sessionID,
		prefix+conversationNoticeAttention+kind+":"+requestID, kind)
}
