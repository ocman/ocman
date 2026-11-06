import React, { useState, useEffect, useRef } from 'react';
import './AssistantThread.css';
import {
  ThreadPrimitive,
  MessagePrimitive,
} from '@assistant-ui/react';

import { useModelLabel, useTurnStats } from '../lib/turnStats';
import { shouldRenderAssistantMessage } from './assistantMessageVisibility';
import { useAgentColor } from '../lib/agentColor';
import { useShortcut } from '../lib/shortcutRegistry';
import { observeMessageLinks } from '../lib/linkHardener';
import { useFailedSends } from '../lib/failedSendsContext';
import { isMutedTool } from '../lib/mutedTools';
import { useStickyBottom } from '../lib/useStickyBottom';
import { useHistoryPagination } from '../lib/useHistoryPagination';
import { useHistoryScrollAnchor } from '../lib/useHistoryScrollAnchor';
import { countRender } from '../lib/perfMonitor';
import { useUiStore } from '../lib/uiStore';
import type { FC } from 'react';
import { LinkPreviewStrip } from './GitHubLinkPreview';
import { MarkdownText } from './assistant/MarkdownText';
import { PreviewImage } from './ImagePreview';
import { LocalCommand } from './assistant/LocalCommand';
import { parseLocalCommand } from '../lib/localCommand';
import { parseTaskNotification } from '../lib/taskNotification';
import { parseAgentMessage } from '../lib/agentMessage';
import { parseSkillInstructions } from '../lib/skillInstructions';
import { TaskNotification } from './assistant/TaskNotification';
import { parseV2Notification } from '../lib/v2Notification';
import { V2Notification } from './assistant/V2Notification';
import { parsePRWatchNotification } from '../lib/prWatchNotification';
import { PRWatchNotification } from './assistant/PRWatchNotification';
import { ToolCallDisplay } from './assistant/ToolCallDisplay';
import { AssistantMeta } from './assistant/AssistantMeta';
import { TurnSummaryBar } from './assistant/TurnSummaryBar';
import { useMessage } from './assistant/useMessage';
import { ExplorationContent } from './assistant/ExplorationContent';
import { ModelLabel } from './ModelLogo';
import { TurnSpeechContext } from '../lib/turnSpeech';
import { TimelineMarker } from './assistant/TimelineMarker';
import { ConversationLaunchContext } from './assistant/ConversationLaunchContext';

interface MessageBookmarkContextValue {
  bookmarkedIds: Set<string>;
  onToggleBookmark?: (messageId: string) => void;
}

const EMPTY_BOOKMARK_IDS = new Set<string>();
const MessageBookmarkContext = React.createContext<MessageBookmarkContextValue>({
  bookmarkedIds: EMPTY_BOOKMARK_IDS,
});

function MessageBookmarkButton({ messageId }: { messageId: string }) {
  const { bookmarkedIds, onToggleBookmark } = React.useContext(MessageBookmarkContext);
  if (!onToggleBookmark) return null;
  const bookmarked = bookmarkedIds.has(messageId);
  return (
    <button
      type="button"
      className={`oc-msg-bookmark-btn${bookmarked ? ' active' : ''}`}
      onClick={(e) => {
        e.preventDefault();
        e.stopPropagation();
        onToggleBookmark(messageId);
      }}
      title={bookmarked ? 'Remove bookmark' : 'Bookmark message'}
      aria-label={bookmarked ? 'Remove bookmark' : 'Bookmark message'}
      aria-pressed={bookmarked}
    >
      <i className={`bi ${bookmarked ? 'bi-bookmark-fill' : 'bi-bookmark'}`} aria-hidden="true" />
    </button>
  );
}

/**
 * Renders a duration badge for a tool card. For completed tools it
 * shows a static duration; for running tools it shows a live elapsed
 * counter that ticks every second.
 */

/**
 * An attached conversation image. Clicking it opens the same zoomable
 * preview modal MarkdownText uses for markdown images.
 *
 * Exported for tests: the thread renders it through assistant-ui's part
 * components, which the suite mocks out.
 */
export const ImageDisplay: FC<{ image: string; filename?: string }> = ({ image, filename }) => {
  const label = filename || 'Image';
  return (
    <div className="oc-image-wrap">
      <PreviewImage buttonClassName="oc-image-toggle" src={image} alt={label} className="oc-image" loading="lazy" />
      {filename && <div className="oc-image-label">{filename}</div>}
    </div>
  );
};

/**
 * `/skill` rendered inline with its arguments. A <details> can't do this:
 * Chrome lays out the hidden content as a block even when the element is
 * inline, pushing the arguments onto the next line.
 */
const SkillCall: FC<{ name: string; instructions: string }> = ({ name, instructions }) => {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        className="oc-read-line oc-skill-called"
        aria-label={`/${name}`}
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <span className="oc-read-arrow" aria-hidden="true">→</span> Skill "{name}"
      </button>
      {open && <div className="oc-skill-called-body">{instructions}</div>}
    </>
  );
};

const UserTextPart: FC<{ text: string }> = ({ text }) => {
  const prWatch = parsePRWatchNotification(text);
  if (prWatch) return <PRWatchNotification notification={prWatch} />;
  const agentMessage = parseAgentMessage(text);
  if (agentMessage) return <TaskNotification notification={agentMessage} />;
  const v2Notification = parseV2Notification(text);
  if (v2Notification) return <V2Notification notification={v2Notification} />;
  const notification = parseTaskNotification(text);
  if (notification) return <TaskNotification notification={notification} />;
  const localCommand = parseLocalCommand(text);
  if (localCommand) return <LocalCommand entry={localCommand} />;
  if (!text.trim()) return null;
  const skill = parseSkillInstructions(text);
  if (skill) {
    return (
      <>
        <SkillCall name={skill.name} instructions={skill.instructions} />
        {skill.rest && <> <UserTextPart text={skill.rest} /></>}
      </>
    );
  }
  return (
    <>
      <span style={{ whiteSpace: 'pre-wrap' }}>{text}</span>
      <LinkPreviewStrip text={text} />
    </>
  );
};

const UserMessage: FC = () => {
  const launch = React.useContext(ConversationLaunchContext);
  const content = useMessage((m) => m.content);
  const localCommand = content.length === 1 && content[0].type === 'text' && parseLocalCommand(content[0].text);
  const id = useMessage((m) => m.id);
  const custom = useMessage((m) => m.metadata?.custom as Record<string, unknown> | undefined);
  const agent = typeof custom?.agent === 'string' ? (custom.agent as string) : undefined;
  const modelChangedTo = typeof custom?.modelChangedTo === 'string' ? (custom.modelChangedTo as string) : undefined;
  const timelineAt = typeof custom?.timelineAt === 'number' ? custom.timelineAt : 0;
  const displayedTimeline = launch?.messageId === id ? launch.timestamp ?? timelineAt : timelineAt;
  const failed = (custom?.failed && typeof custom.failed === 'object')
    ? (custom.failed as { error?: string; imagesDropped?: boolean })
    : undefined;
  const failedSendsCtx = useFailedSends();
  // Failed sends get a danger-tinted border so the banner reads as
  // attached to the right bubble; otherwise use the agent color.
  const agentBorder = useAgentColor(agent);
  let borderStyle: React.CSSProperties | undefined;
  if (failed) {
    borderStyle = { borderLeftColor: 'var(--danger)' };
  } else if (agent) {
    borderStyle = { borderLeftColor: agentBorder };
  }
  const hasContent = content.some(
    (p) => (p.type === 'text' && 'text' in p && (p as { text: string }).text.trim()) || p.type === 'tool-call' || p.type === 'image'
  );
  // A failed send is worth showing even when its body is empty — the banner
  // itself carries the action the user needs.
  if (!hasContent && !failed) return null;
  const systemNotification = !failed && content.every((p) => p.type === 'text' && (parsePRWatchNotification(p.text) || parseAgentMessage(p.text) || parseV2Notification(p.text) || parseTaskNotification(p.text) || parseSkillInstructions(p.text)?.rest === ''));
  const onlyTools = systemNotification && content.every((p) => p.type === 'text' &&
    (parseV2Notification(p.text)?.kind === 'shell' || parseSkillInstructions(p.text)?.rest === ''));

  return (
    <>
      {!!displayedTimeline && <TimelineMarker timestamp={displayedTimeline} />}
      {modelChangedTo && <ModelChangeDivider model={modelChangedTo} />}
    <MessagePrimitive.Root
      className={`oc-msg ${systemNotification ? 'oc-msg-system' : 'oc-msg-user'}${onlyTools ? ' oc-msg-tools' : ''}${localCommand && !failed ? ' oc-msg-local-command' : ''}${failed ? ' oc-msg-failed' : ''}`}
      data-message-id={id}
      style={systemNotification ? undefined : borderStyle}
    >
      {!systemNotification && <MessageBookmarkButton messageId={id} />}
      <div className="oc-msg-body">
        <MessagePrimitive.Content components={USER_PART_COMPONENTS} />
      </div>
      {failed && id && (
        <div className="oc-msg-failed-banner" role="alert">
          <i className="bi bi-exclamation-triangle-fill" aria-hidden="true" />
          <span className="oc-msg-failed-text">
            Failed to send{failed.error ? `: ${failed.error}` : ''}
            {failed.imagesDropped && (
              <span className="oc-msg-failed-hint"> (images couldn{'\u2019'}t be saved across refresh)</span>
            )}
          </span>
          <span className="oc-msg-failed-actions">
            <button
              type="button"
              className="oc-msg-failed-btn oc-msg-failed-retry"
              onClick={() => failedSendsCtx.retry(id)}
            >Retry</button>
            <button
              type="button"
              className="oc-msg-failed-btn oc-msg-failed-dismiss"
              onClick={() => failedSendsCtx.dismiss(id)}
              aria-label="Dismiss failed message"
            >Dismiss</button>
          </span>
        </div>
      )}
    </MessagePrimitive.Root>
    {launch?.messageId === id && launch.setup}
    </>
  );
};

const AssistantMessage: FC = () => {
  const content = useMessage((m) => m.content);
  const messageId = useMessage((m) => m.id);
  const custom = useMessage((m) => m.metadata?.custom as Record<string, unknown> | undefined);
  const modelChangedTo = typeof custom?.modelChangedTo === 'string' ? (custom.modelChangedTo as string) : undefined;
  const timelineAt = typeof custom?.timelineAt === 'number' ? custom.timelineAt : 0;
  const turnStats = useTurnStats();
  const hasContent = content.some(
    (p) => (p.type === 'text' && 'text' in p && (p as { text: string }).text.trim()) || p.type === 'tool-call' || p.type === 'image'
  );
  if (!shouldRenderAssistantMessage(hasContent, turnStats?.isSummaryAnchor ?? false)) return null;

  // Messages that only contain muted tool calls (reads/greps/webfetch) render as a compact list
  const onlyMuted = content.every(
    (p) => {
      if (p.type === 'text' && 'text' in p && !(p as { text: string }).text.trim()) return true;
      if (p.type !== 'tool-call' || !('toolName' in p)) return false;
      return isMutedTool((p as { toolName: string }).toolName);
    }
  );
  const onlyTools = content.every((p) => p.type === 'tool-call' || (p.type === 'text' && !p.text.trim()));

  if (onlyMuted) {
    return (
      <>
        {!!timelineAt && <TimelineMarker timestamp={timelineAt} />}
        {modelChangedTo && <ModelChangeDivider model={modelChangedTo} />}
        <MessagePrimitive.Root className="oc-msg oc-msg-muted" data-message-id={messageId}>
          <ExplorationContent components={ASSISTANT_PART_COMPONENTS} />
          <TurnSummaryBar actions={<TurnActions messageId={messageId} />} />
        </MessagePrimitive.Root>
      </>
    );
  }

  return (
    <>
      {!!timelineAt && <TimelineMarker timestamp={timelineAt} />}
      {modelChangedTo && <ModelChangeDivider model={modelChangedTo} />}
      <MessagePrimitive.Root className={`oc-msg oc-msg-assistant${onlyTools ? ' oc-msg-tools' : ''}`} data-message-id={messageId}>
        <div className="oc-msg-body oc-md">
          <ExplorationContent components={ASSISTANT_PART_COMPONENTS} />
        </div>
        <AssistantMeta />
        <TurnSummaryBar actions={<TurnActions messageId={messageId} />} />
      </MessagePrimitive.Root>
    </>
  );
};

/**
 * Centered divider chip rendered before the first assistant message that
 * switches the conversation to a new model. Mirrors the look of an
 * in-thread date separator so model changes are easy to scan.
 */
const ModelChangeDivider: FC<{ model: string }> = ({ model }) => {
  const label = useModelLabel(model);
  return (
    <div className="oc-model-change" role="separator" data-testid="model-change-divider">
      <span className="oc-model-change-line" aria-hidden="true" />
      <span className="oc-model-change-chip" title={model}>
        Model changed to <ModelLabel model={model}>{label}</ModelLabel>
      </span>
      <span className="oc-model-change-line" aria-hidden="true" />
    </div>
  );
};

/** Read-aloud and bookmark controls shown on a finished turn's summary bar. */
function TurnActions({ messageId }: { messageId: string }) {
  const speech = React.useContext(TurnSpeechContext);
  return (
    <>
      {speech.answers.has(messageId) && <button
        type="button"
        className="oc-turn-speech-btn"
        aria-label={speech.speakingId === messageId ? 'Stop reading' : 'Read aloud'}
        title={speech.speakingId === messageId ? 'Stop reading' : 'Read aloud'}
        onClick={() => speech.toggle(messageId)}
      >
        <i className={`bi ${speech.speakingId === messageId ? 'bi-stop-fill' : 'bi-volume-up'}`} aria-hidden="true" />
      </button>}
      <MessageBookmarkButton messageId={messageId} />
    </>
  );
}

// Module-scoped component maps. MessagePrimitive.Content and
// ThreadPrimitive.Messages memoize internally on the `components`
// identity — a fresh object each render busts that cache for every
// mounted message. Declared here (after all referenced FCs) to keep
// references stable across renders.
const USER_PART_COMPONENTS = { Text: UserTextPart, Image: ImageDisplay };
const ASSISTANT_PART_COMPONENTS = {
  Text: MarkdownText,
  Image: ImageDisplay,
  tools: { Fallback: ToolCallDisplay },
};
const THREAD_MESSAGE_COMPONENTS = { UserMessage, AssistantMessage };


export function AssistantThread({
  hasMore,
  oldestMessageId,
  loadingMore,
  onLoadMore,
  composer,
  footer,
  empty,
  bookmarkedMessageIds,
  onToggleMessageBookmark,
  scrollToMessageId,
  scrollToMessageTick,
  scrollToBottomTick,
  scrollToToolCall,
}: {
  hasMore?: boolean;
  oldestMessageId?: string;
  loadingMore?: boolean;
  onLoadMore?: () => void;
  composer?: React.ReactNode;
  footer?: React.ReactNode;
  /** Handoff content shown even when message headers arrive before their parts. */
  empty?: React.ReactNode;
  bookmarkedMessageIds?: Set<string>;
  onToggleMessageBookmark?: (messageId: string) => void;
  scrollToMessageId?: string | null;
  scrollToMessageTick?: number;
  scrollToBottomTick?: number;
  scrollToToolCall?: { messageId: string; toolCallId: string; tick: number } | null;
}) {
  countRender('AssistantThread');
  const showToolDetails = useUiStore((s) => s.showToolDetails);
  const speech = React.useContext(TurnSpeechContext);
  const threadRef = useRef<HTMLDivElement>(null);
  const viewportRef = useRef<HTMLDivElement>(null);
  const loadMore = useHistoryScrollAnchor(viewportRef, loadingMore, onLoadMore);
  const hasMoreRef = useRef(hasMore);
  const loadingMoreRef = useRef(loadingMore);
  const onLoadMoreRef = useRef(loadMore);
  useEffect(() => { hasMoreRef.current = hasMore; }, [hasMore]);
  useEffect(() => { loadingMoreRef.current = loadingMore; }, [loadingMore]);
  useEffect(() => { onLoadMoreRef.current = loadMore; }, [loadMore]);

  // Safety net for any non-markdown-rendered links in message bodies.
  // Markdown links are handled at render time by MarkdownLink; this catches
  // anything else that might land in the DOM with a raw href.
  //
  // Coalesce added subtrees without rescanning historical messages.
  useEffect(() => {
    const thread = threadRef.current;
    if (!thread) return;
    return observeMessageLinks(thread);
  }, []);

  const isJumpingRef = useRef(false);
  useEffect(() => {
    isJumpingRef.current = false;
  }, [hasMore, loadingMore]);

  const historyStartRef = useHistoryPagination(viewportRef, { oldestMessageId, hasMore, loadingMore, onLoadMore: loadMore });

  // Companion auto-scroll, replacing ThreadPrimitive.Viewport's built-in
  // `autoScroll` (disabled below): its hardcoded 1px at-bottom tolerance
  // races streaming DOM growth. useStickyBottom follows the tail until
  // the user gestures away from it, and drives the scroll-to-bottom
  // affordance. See lib/useStickyBottom.ts.
  const { showScrollToBottom, scrollToBottom } = useStickyBottom(viewportRef);
  useEffect(() => {
    if (scrollToBottomTick) scrollToBottom();
  }, [scrollToBottomTick, scrollToBottom]);

  // Alt+H / Alt+L (Option+H / Option+L on Mac) jump between user messages in
  // the history. Alt+H: previous user message (up). Alt+L: next user message
  // (down). The registry handles key-matching and preventDefault; this code
  // just computes the scroll target.
  //
  // Alt+L past the last user message scrolls to the very bottom of the
  // viewport so the most recent assistant response is visible — otherwise we'd
  // re-snap to the last user message and leave the response hidden below.
  const jumpToUserMessage = (direction: 'prev' | 'next') => {
    const viewport = viewportRef.current;
    if (!viewport) return;

    const userMsgs = Array.from(viewport.querySelectorAll<HTMLElement>('.oc-msg-user'));
    if (userMsgs.length === 0) {
      if (direction === 'next') {
        viewport.scrollTo({ top: viewport.scrollHeight, behavior: 'auto' });
      }
      return;
    }

    const viewportTop = viewport.getBoundingClientRect().top;
    const epsilon = 4;
    const offsets = userMsgs.map((el) => el.getBoundingClientRect().top - viewportTop);

    if (direction === 'next') {
      // Find the first message that's clearly below the current viewport position.
      // We look for messages with offset > 50px to skip any message that's currently
      // at the top and find the next one down.
      const nextIdx = offsets.findIndex((o) => o > 50);
      if (nextIdx === -1) {
        viewport.scrollTo({ top: viewport.scrollHeight, behavior: 'auto' });
        return;
      }
      const target = userMsgs[nextIdx];
      const targetTop = target.getBoundingClientRect().top - viewportTop + viewport.scrollTop;
      viewport.scrollTo({ top: Math.max(0, targetTop - 12), behavior: 'auto' });
      return;
    }

    // direction === 'prev'
    let targetIndex = -1;
    for (let i = offsets.length - 1; i >= 0; i--) {
      if (offsets[i] < -epsilon) { targetIndex = i; break; }
    }
    if (targetIndex === -1) {
      if (viewport.scrollTop > 100 && hasMoreRef.current && !loadingMoreRef.current && !isJumpingRef.current) {
        isJumpingRef.current = true;
        onLoadMoreRef.current?.();
      }
      return;
    }
    const target = userMsgs[targetIndex];
    const targetTop = target.getBoundingClientRect().top - viewportTop + viewport.scrollTop;
    viewport.scrollTo({ top: Math.max(0, targetTop - 12), behavior: 'auto' });
  };

  const prevUserMessageShortcut = {
    id: 'session.prev-user-message',
    scope: 'session' as const,
    keys: { code: 'KeyH', alt: true },
    description: 'Jump to previous user message',
    runInEditable: true,
    handler: () => jumpToUserMessage('prev'),
  };

  const nextUserMessageShortcut = {
    id: 'session.next-user-message',
    scope: 'session' as const,
    keys: { code: 'KeyL', alt: true },
    description: 'Jump to next user message',
    runInEditable: true,
    handler: () => jumpToUserMessage('next'),
  };

  useShortcut(prevUserMessageShortcut);
  useShortcut(nextUserMessageShortcut);

  // Merge our viewportRef with the library's auto-scroll ref callback.
  // ThreadPrimitive.Viewport provides auto-scroll, isAtBottom tracking,
  // and content-resize handling out of the box.
  const setViewportRef = (el: HTMLDivElement | null) => {
    (viewportRef as React.MutableRefObject<HTMLDivElement | null>).current = el;
  };

  useEffect(() => {
    if (!scrollToMessageId || !scrollToMessageTick) return;
    const viewport = viewportRef.current;
    if (!viewport) return;
    const escaped = CSS.escape(scrollToMessageId);
    const target = viewport.querySelector<HTMLElement>(`[data-message-id="${escaped}"]`);
    if (!target) {
      if (hasMore && !loadingMore) void onLoadMore?.();
      return;
    }
    const exploration = target.closest('details.oc-exploration');
    if (exploration instanceof HTMLDetailsElement) exploration.open = true;
    const viewportTop = viewport.getBoundingClientRect().top;
    const targetTop = target.getBoundingClientRect().top - viewportTop + viewport.scrollTop;
    viewport.scrollTo({ top: Math.max(0, targetTop - 12), behavior: 'smooth' });
    target.classList.add('oc-msg-scroll-highlight');
    const timeout = setTimeout(() => target.classList.remove('oc-msg-scroll-highlight'), 1200);
    return () => clearTimeout(timeout);
  }, [scrollToMessageId, scrollToMessageTick, hasMore, loadingMore, onLoadMore]);

  useEffect(() => {
    if (!scrollToToolCall) return;
    const viewport = viewportRef.current;
    if (!viewport) return;
    const message = viewport.querySelector<HTMLElement>(`[data-message-id="${CSS.escape(scrollToToolCall.messageId)}"]`);
    const source = message?.querySelector<HTMLElement>(`[data-tool-call-id="${CSS.escape(scrollToToolCall.toolCallId)}"]`);
    if (!source?.firstElementChild) return;
    const exploration = source.closest('details.oc-exploration');
    if (exploration instanceof HTMLDetailsElement) exploration.open = true;
    viewport.querySelectorAll('.oc-tool-source-revealed').forEach((node) => node.classList.remove('oc-tool-source-revealed'));
    source.classList.add('oc-tool-source-revealed');
    source.querySelector<HTMLButtonElement>('[aria-expanded="false"]')?.click();
    let timeout: ReturnType<typeof setTimeout> | undefined;
    const frame = requestAnimationFrame(() => {
      const target = source.firstElementChild as HTMLElement | null;
      if (!target) return;
      const viewportTop = viewport.getBoundingClientRect().top;
      const targetTop = target.getBoundingClientRect().top - viewportTop + viewport.scrollTop;
      viewport.scrollTo({ top: Math.max(0, targetTop - 12), behavior: 'smooth' });
      target.classList.add('oc-msg-scroll-highlight');
      target.tabIndex = -1;
      target.focus({ preventScroll: true });
      timeout = setTimeout(() => target.classList.remove('oc-msg-scroll-highlight'), 1200);
    });
    return () => {
      cancelAnimationFrame(frame);
      if (timeout) clearTimeout(timeout);
    };
  }, [scrollToToolCall]);

  // Track the composer height so the scroll-to-bottom button
  // (positioned absolute inside .oc-thread) can float just above it.
  // RAF-coalesced to avoid layout thrash when the textarea resizes on
  // every keystroke.
  const footerRef = (el: HTMLDivElement | null) => {
    if (!el) return;
    let rafId = 0;
    const update = () => {
      if (rafId) return;
      rafId = requestAnimationFrame(() => {
        rafId = 0;
        const thread = el.closest('.oc-thread') as HTMLElement | null;
        if (thread) thread.style.setProperty('--oc-footer-height', `${el.offsetHeight}px`);
      });
    };
    update();
    const ro = new ResizeObserver(update);
    ro.observe(el);
    return () => {
      ro.disconnect();
      cancelAnimationFrame(rafId);
    };
  };

  // eslint-disable-next-line react/jsx-no-constructed-context-values -- React Compiler memoizes bookmarkContextValue.
  const bookmarkContextValue = {
    bookmarkedIds: bookmarkedMessageIds ?? EMPTY_BOOKMARK_IDS,
    onToggleBookmark: onToggleMessageBookmark,
  };

  return (
    <div ref={threadRef} style={{ display: 'flex', flex: 1, minHeight: 0 }}>
      <MessageBookmarkContext.Provider value={bookmarkContextValue}>
      <ThreadPrimitive.Root className={`oc-thread${showToolDetails ? '' : ' oc-hide-tool-details'}`}>
        {/* Disable the library's built-in auto-scroll, which uses a
             1px at-bottom tolerance that races with streaming DOM growth and
             snaps the viewport down even when the user has scrolled up to
             read. useStickyBottom owns all auto-scroll with an 80px band that
             respects a deliberate scroll-up.

             `autoScroll={false}` is not enough on its own:
             `scrollToBottomOnRunStart` defaults to true and is checked
             independently of it. Worse, it does not just scroll once — it
             latches `scrollingToBottomBehaviorRef`, which the library's
             content-resize handler then re-applies on *every* subsequent
             resize. That latch is only cleared by a scroll event that lands
             at the bottom, so scrolling up never clears it: one run start
             means every later streaming chunk drags the reader back down.

             `scrollToBottomOnInitialize` / `scrollToBottomOnThreadSwitch` are
             deliberately left enabled — they fire at mount, which is the one
             moment the tail *is* where the user wants to be, and they are
             what opens a conversation at the bottom today. */}
        <ThreadPrimitive.Viewport
          ref={setViewportRef}
          className="oc-thread-viewport"
          data-testid="conversation-viewport"
          autoScroll={false}
          scrollToBottomOnRunStart={false}
        >
          {hasMore && loadingMore && (
            <div className="oc-load-more">
              <span className="oc-spinner" /> Loading older messages...
            </div>
          )}
          <div ref={historyStartRef} aria-hidden="true" />
          {empty || <ThreadPrimitive.Empty>
            <div className="oc-empty">No messages yet.</div>
          </ThreadPrimitive.Empty>}
          <ThreadPrimitive.Messages components={THREAD_MESSAGE_COMPONENTS} />
          {speech.error && <div role="status" className="oc-thread-error-note">{speech.error}</div>}
          {footer && <div className="oc-thread-footer">{footer}</div>}
        </ThreadPrimitive.Viewport>
        <div ref={footerRef} className="oc-viewport-footer" data-testid="conversation-composer">
          {composer}
        </div>
        {/* Our own affordance rather than ThreadPrimitive.ScrollToBottom:
             the primitive's visibility comes from the library's 1px
             at-bottom check, which flips off and on with every streaming
             size jump. That was previously papered over with a 400ms CSS
             show-delay; useStickyBottom drives this from a hysteresis
             band instead, so it is stable without a timer and appears
             immediately on a real scroll-up. */}
        {showScrollToBottom && (
          <button
            type="button"
            className="oc-scroll-btn"
            data-testid="scroll-to-bottom"
            onClick={scrollToBottom}
          >
            Scroll to bottom
          </button>
        )}
      </ThreadPrimitive.Root>
      </MessageBookmarkContext.Provider>
    </div>
  );
}
