import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import './AssistantThread.css';
import {
  ThreadPrimitive,
  MessagePrimitive,
  useMessage,
} from '@assistant-ui/react';
import { formatSeconds, formatTokensPerSecond, formatCompactNumber, formatCurrency } from '../lib/format';
import { useModelLabel, useTurnStats } from '../lib/turnStats';
import { shouldRenderAssistantMessage } from './assistantMessageVisibility';
import { useAgentColor } from '../lib/agentColor';
import { useShortcut } from '../lib/shortcutRegistry';
import { hardenMessageLinks } from '../lib/linkHardener';
import { useFailedSends } from '../lib/failedSendsContext';
import { isMutedTool } from '../lib/mutedTools';
import { useStickyBottom } from '../lib/useStickyBottom';
import { trackRender } from '../lib/renderRateMonitor';
import { useUiStore } from '../lib/uiStore';
import type { FC } from 'react';
import { LinkPreviewStrip } from './GitHubLinkPreview';
import { MarkdownText } from './assistant/MarkdownText';
import { ToolCallDisplay } from './assistant/ToolCallDisplay';

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

const ImageDisplay: FC<{ image: string; filename?: string }> = ({ image, filename }) => {
  const [expanded, setExpanded] = useState(false);
  return (
    <div className="oc-image-wrap">
      <img
        src={image}
        alt={filename || 'Image'}
        className={`oc-image${expanded ? ' oc-image-expanded' : ''}`}
        onClick={() => setExpanded(!expanded)}
        loading="lazy"
      />
      {filename && <div className="oc-image-label">{filename}</div>}
    </div>
  );
};

const UserTextPart: FC<{ text: string }> = ({ text }) => {
  if (!text.trim()) return null;
  return (
    <>
      <span style={{ whiteSpace: 'pre-wrap' }}>{text}</span>
      <LinkPreviewStrip text={text} />
    </>
  );
};

const UserMessage: FC = () => {
  const content = useMessage((m) => m.content);
  const id = useMessage((m) => m.id);
  const custom = useMessage((m) => m.metadata?.custom as Record<string, unknown> | undefined);
  const agent = typeof custom?.agent === 'string' ? (custom.agent as string) : undefined;
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

  return (
    <MessagePrimitive.Root
      className={`oc-msg oc-msg-user${failed ? ' oc-msg-failed' : ''}`}
      data-message-id={id}
      style={borderStyle}
    >
      <MessageBookmarkButton messageId={id} />
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
  );
};

const AssistantMessage: FC = () => {
  const content = useMessage((m) => m.content);
  const messageId = useMessage((m) => m.id);
  const custom = useMessage((m) => m.metadata?.custom as Record<string, unknown> | undefined);
  const modelChangedTo = typeof custom?.modelChangedTo === 'string' ? (custom.modelChangedTo as string) : undefined;
  const turnStats = useTurnStats(messageId);
  const hasContent = content.some(
    (p) => (p.type === 'text' && 'text' in p && (p as { text: string }).text.trim()) || p.type === 'tool-call' || p.type === 'image'
  );
  const isLiveSummaryAnchor = (turnStats?.isLive && turnStats?.isSummaryAnchor) ?? false;
  if (!shouldRenderAssistantMessage(hasContent, isLiveSummaryAnchor)) return null;

  // Messages that only contain muted tool calls (reads/greps/webfetch) render as a compact list
  const onlyMuted = content.every(
    (p) => {
      if (p.type === 'text' && 'text' in p && !(p as { text: string }).text.trim()) return true;
      if (p.type !== 'tool-call' || !('toolName' in p)) return false;
      return isMutedTool((p as { toolName: string }).toolName);
    }
  );

  if (onlyMuted) {
    return (
      <>
        {modelChangedTo && <ModelChangeDivider model={modelChangedTo} />}
        <MessagePrimitive.Root className="oc-msg oc-msg-muted" data-message-id={messageId}>
          <MessageBookmarkButton messageId={messageId} />
          <MessagePrimitive.Content components={ASSISTANT_PART_COMPONENTS} />
          <TurnSummaryBar messageId={messageId} />
        </MessagePrimitive.Root>
      </>
    );
  }

  return (
    <>
      {modelChangedTo && <ModelChangeDivider model={modelChangedTo} />}
      <MessagePrimitive.Root className="oc-msg oc-msg-assistant" data-message-id={messageId}>
        <MessageBookmarkButton messageId={messageId} />
        <div className="oc-msg-body oc-md">
          <MessagePrimitive.Content components={ASSISTANT_PART_COMPONENTS} />
        </div>
        <AssistantMeta />
        <TurnSummaryBar messageId={messageId} />
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
        <i className="bi bi-cpu" aria-hidden="true" />
        Model changed to {label}
      </span>
      <span className="oc-model-change-line" aria-hidden="true" />
    </div>
  );
};

function AssistantMeta() {
  const showMessageMetadata = useUiStore((s) => s.showMessageMetadata);
  const createdAt = useMessage((m) => m.createdAt);
  const status = useMessage((m) => m.status);
  const content = useMessage((m) => m.content);
  const custom = useMessage((m) => m.metadata?.custom as Record<string, unknown> | undefined);
  const agent = typeof custom?.agent === 'string' ? (custom.agent as string) : undefined;
  const model = typeof custom?.model === 'string' ? (custom.model as string) : '';
  const modelLabel = useModelLabel(model);
  if (!createdAt || createdAt.getTime() === 0) return null;
  if (status?.type === 'running') return null;
  // Hide timestamp when message only contains file reads
  const onlyReads = content.every(
    (p) => {
      if (p.type === 'text' && 'text' in p && !(p as { text: string }).text.trim()) return true;
      if (p.type !== 'tool-call' || !('toolName' in p)) return false;
      return isMutedTool((p as { toolName: string }).toolName);
    }
  );
  if (onlyReads) return null;
  const time = createdAt.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
  const isError = status?.type === 'incomplete' && 'reason' in status && status.reason === 'error';
  const errorName = custom?.errorName as string | undefined;
  const isAbort = errorName === 'MessageAbortedError' || errorName === 'AbortError';

  // Compute duration and tokens-per-second from per-message timing data when available.
  const msgTime = custom?.time as { created?: number; completed?: number } | undefined;
  const msgTokens = custom?.tokens as { output?: number } | undefined;
  let durationSec: number | null = null;
  let tps: number | null = null;
  if (msgTime?.created && msgTime?.completed) {
    const d = (msgTime.completed - msgTime.created) / 1000;
    if (d > 0) {
      durationSec = d;
      if (msgTokens?.output) tps = msgTokens.output / d;
    }
  }

  return (
    <>
      {isError && (
        <div className={`oc-error-banner${isAbort ? ' oc-error-banner-abort' : ''}`}>
          {isAbort
            ? <><i className="bi bi-slash-circle" aria-hidden="true" /> Interrupted</>
            : <><i className="bi bi-exclamation-triangle-fill" aria-hidden="true" /> Session ended with an error</>
          }
        </div>
      )}
      {showMessageMetadata && <div className="oc-msg-meta">
        <span
          className="oc-meta-dot"
          style={isError ? { background: 'var(--danger)' } : undefined}
          title={isError ? 'Error' : agent ? `agent: ${agent}` : 'Message group'}
        />
        <span>{time}</span>
        {durationSec !== null && (
          <>
            <span className="oc-meta-sep">·</span>
            <span className="oc-meta-tps">{formatSeconds(durationSec)}</span>
          </>
        )}
        {tps !== null && (
          <>
            <span className="oc-meta-sep">·</span>
            <span className="oc-meta-tps">{formatTokensPerSecond(tps)} tok/s</span>
          </>
        )}
        {model && (
          <>
            <span className="oc-meta-sep">·</span>
            <span className="oc-meta-model" title={model}>
              <i className="bi bi-cpu" aria-hidden="true" />
              {modelLabel}
            </span>
          </>
        )}
      </div>}
    </>
  );
}

/**
 * Always-visible summary bar rendered after the last assistant message of
 * each turn. Shows wall-clock duration, total tokens in+out, tool-call
 * count, and average tok/s. While the turn is still in progress the bar
 * shows whatever data is available so far.
 */
function TurnSummaryBar({ messageId }: { messageId: string }) {
  const stats = useTurnStats(messageId);
  const custom = useMessage((m) => m.metadata?.custom as Record<string, unknown> | undefined);
  const agent = typeof custom?.agent === 'string' ? (custom.agent as string) : undefined;
  const agentColor = useAgentColor(agent);
  const modelLabel = useModelLabel(stats?.model || '');
  const [now, setNow] = useState(Date.now);

  // Tick every second so the live wall-clock increments visibly.
  useEffect(() => {
    if (!stats?.isLive) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [stats?.isLive]);

  // Only the turn's anchor message renders the bar. Non-anchor messages
  // still carry the aggregate (so the line never blanks out while
  // ownership moves between messages mid-turn) but must not draw it.
  if (!stats || !stats.isSummaryAnchor) return null;

  const { wallClockMs, tokensOut, tokensIn, cost, toolCalls, tps, isLive, startedAt, model } = stats;

  // For a live turn, compute elapsed wall-clock from startedAt → now.
  const displayMs = isLive ? now - startedAt : wallClockMs;
  const wallSec = displayMs !== null && displayMs > 0 ? displayMs / 1000 : null;

  const totalTokens = tokensIn + tokensOut;

  const time = new Date(startedAt).toLocaleTimeString('en-US', {
    hour: '2-digit', minute: '2-digit', hour12: false,
  });

  const items: React.ReactNode[] = [];
  if (!isLive) {
    items.push(
      <span key="time" className="oc-turn-stat">
        {time}
      </span>
    );
  }
  if (wallSec !== null) {
    items.push(
      <span key="wall" className="oc-turn-stat">
        <i className="bi bi-clock" aria-hidden="true" />
        {formatSeconds(wallSec)}
      </span>
    );
  }
  if (totalTokens > 0) {
    items.push(
      <span key="tok" className="oc-turn-stat">
        <i className="bi bi-stars" aria-hidden="true" />
        {formatCompactNumber(totalTokens)} tok
      </span>
    );
  }
  if (toolCalls > 0) {
    items.push(
      <span key="tools" className="oc-turn-stat">
        <i className="bi bi-tools" aria-hidden="true" />
        {toolCalls} {toolCalls === 1 ? 'tool' : 'tools'}
      </span>
    );
  }
  if (tps !== null) {
    items.push(
      <span key="tps" className="oc-turn-stat">
        {formatTokensPerSecond(tps)} tok/s
      </span>
    );
  }
  if (cost > 0) {
    items.push(
      <span key="cost" className="oc-turn-stat">
        {formatCurrency(cost, cost < 0.001 ? 4 : 2)}
      </span>
    );
  }
  if (model) {
    items.push(
      <span key="model" className="oc-turn-stat oc-turn-model" title={model}>
        <i className="bi bi-cpu" aria-hidden="true" />
        {modelLabel}
      </span>
    );
  }

  if (items.length === 0 && !isLive) return null;

  return (
    <div className="oc-turn-stats">
      <span
        className={`oc-turn-dot${isLive ? ' oc-turn-dot--live' : ''}`}
        style={agent ? { background: agentColor } : undefined}
        title={isLive ? 'Turn in progress' : undefined}
      />
      {items.map((item, i) => (
        <React.Fragment key={i}>
          {item}
          {i < items.length - 1 && <span className="oc-turn-sep">·</span>}
        </React.Fragment>
      ))}
    </div>
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
  loadingMore,
  onLoadMore,
  composer,
  footer,
  bookmarkedMessageIds,
  onToggleMessageBookmark,
  scrollToMessageId,
  scrollToMessageTick,
}: {
  hasMore?: boolean;
  loadingMore?: boolean;
  onLoadMore?: () => void;
  composer?: React.ReactNode;
  footer?: React.ReactNode;
  bookmarkedMessageIds?: Set<string>;
  onToggleMessageBookmark?: (messageId: string) => void;
  scrollToMessageId?: string | null;
  scrollToMessageTick?: number;
}) {
  trackRender('AssistantThread');
  const showToolDetails = useUiStore((s) => s.showToolDetails);
  const threadRef = useRef<HTMLDivElement>(null);
  const viewportRef = useRef<HTMLDivElement>(null);
  const hasMoreRef = useRef(hasMore);
  const loadingMoreRef = useRef(loadingMore);
  const onLoadMoreRef = useRef(onLoadMore);
  useEffect(() => { hasMoreRef.current = hasMore; }, [hasMore]);
  useEffect(() => { loadingMoreRef.current = loadingMore; }, [loadingMore]);
  useEffect(() => { onLoadMoreRef.current = onLoadMore; }, [onLoadMore]);

  // Safety net for any non-markdown-rendered links in message bodies.
  // Markdown links are handled at render time by MarkdownLink; this catches
  // anything else that might land in the DOM with a raw href.
  //
  // Debounced via requestIdleCallback (with a 200ms timeout fallback) so
  // the link scan runs at most once per idle period instead of on every
  // DOM mutation during streaming (P1 fix).
  useEffect(() => {
    const thread = threadRef.current;
    if (!thread) return;
    const apply = () => hardenMessageLinks(thread);
    apply();
    let idleHandle: number | ReturnType<typeof setTimeout> | null = null;
    const scheduleApply = () => {
      if (idleHandle !== null) return;
      if (typeof requestIdleCallback === 'function') {
        idleHandle = requestIdleCallback(() => { idleHandle = null; apply(); }, { timeout: 200 });
      } else {
        idleHandle = setTimeout(() => { idleHandle = null; apply(); }, 200);
      }
    };
    const observer = new MutationObserver(scheduleApply);
    observer.observe(thread, { childList: true, subtree: true });
    return () => {
      observer.disconnect();
      if (idleHandle !== null) {
        if (typeof cancelIdleCallback === 'function' && typeof idleHandle === 'number') {
          cancelIdleCallback(idleHandle);
        } else {
          clearTimeout(idleHandle as ReturnType<typeof setTimeout>);
        }
      }
    };
  }, []);

  const isJumpingRef = useRef(false);
  useEffect(() => {
    isJumpingRef.current = false;
  }, [hasMore, loadingMore]);

  // Auto-load older messages when scrolled near the top of the viewport.
  // Attaches a passive scroll listener to the viewport element.
  useEffect(() => {
    const el = viewportRef.current;
    if (!el) return;
    const onScroll = () => {
      if (el.scrollTop < 200 && hasMoreRef.current && !loadingMoreRef.current) {
        onLoadMoreRef.current?.();
      }
    };
    el.addEventListener('scroll', onScroll, { passive: true });
    return () => el.removeEventListener('scroll', onScroll);
  }, []);

  // Preserve scroll position when older messages are prepended
  const prevScrollHeightRef = useRef(0);
  const wasLoadingRef = useRef(false);
  useEffect(() => {
    const el = viewportRef.current;
    if (!el) return;
    if (loadingMore && !wasLoadingRef.current) {
      // Started loading: save current scroll height
      prevScrollHeightRef.current = el.scrollHeight;
    } else if (!loadingMore && wasLoadingRef.current && prevScrollHeightRef.current > 0) {
      // Finished loading: adjust scroll to maintain position
      requestAnimationFrame(() => {
        const diff = el.scrollHeight - prevScrollHeightRef.current;
        if (diff > 0) {
          el.scrollTop += diff;
        }
        prevScrollHeightRef.current = 0;
      });
    }
    wasLoadingRef.current = !!loadingMore;
  }, [loadingMore]);

  // Companion auto-scroll. ThreadPrimitive.Viewport's built-in
  // `autoScroll` decides "is at bottom" with a hardcoded 1px
  // tolerance, which is too strict for streaming chats: the composer
  // textarea growing or a code block reflowing during streaming
  // routinely leaves the user a few pixels above the bottom, after
  // which the library stops following new messages. useStickyBottom
  // relaxes that to ~80px so the conversation keeps tracking the
  // bottom while the user is "near" it. See lib/useStickyBottom.ts.
  useStickyBottom(viewportRef);

  // Alt+H / Alt+L (Option+H / Option+L on Mac) jump between user messages in
  // the history. Alt+H: previous user message (up). Alt+L: next user message
  // (down). The registry handles key-matching and preventDefault; this code
  // just computes the scroll target.
  //
  // Alt+L past the last user message scrolls to the very bottom of the
  // viewport so the most recent assistant response is visible — otherwise we'd
  // re-snap to the last user message and leave the response hidden below.
  const jumpToUserMessage = useCallback((direction: 'prev' | 'next') => {
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
  }, []);

  const prevUserMessageShortcut = useMemo(() => ({
    id: 'session.prev-user-message',
    scope: 'session' as const,
    keys: { code: 'KeyH', alt: true },
    description: 'Jump to previous user message',
    runInEditable: true,
    handler: () => jumpToUserMessage('prev'),
  }), [jumpToUserMessage]);

  const nextUserMessageShortcut = useMemo(() => ({
    id: 'session.next-user-message',
    scope: 'session' as const,
    keys: { code: 'KeyL', alt: true },
    description: 'Jump to next user message',
    runInEditable: true,
    handler: () => jumpToUserMessage('next'),
  }), [jumpToUserMessage]);

  useShortcut(prevUserMessageShortcut);
  useShortcut(nextUserMessageShortcut);

  // Merge our viewportRef with the library's auto-scroll ref callback.
  // ThreadPrimitive.Viewport provides auto-scroll, isAtBottom tracking,
  // and content-resize handling out of the box.
  const setViewportRef = useCallback((el: HTMLDivElement | null) => {
    (viewportRef as React.MutableRefObject<HTMLDivElement | null>).current = el;
  }, []);

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
    const viewportTop = viewport.getBoundingClientRect().top;
    const targetTop = target.getBoundingClientRect().top - viewportTop + viewport.scrollTop;
    viewport.scrollTo({ top: Math.max(0, targetTop - 12), behavior: 'smooth' });
    target.classList.add('oc-msg-scroll-highlight');
    const timeout = setTimeout(() => target.classList.remove('oc-msg-scroll-highlight'), 1200);
    return () => clearTimeout(timeout);
  }, [scrollToMessageId, scrollToMessageTick, hasMore, loadingMore, onLoadMore]);

  // Track the ViewportFooter height so the scroll-to-bottom button
  // (positioned absolute inside .oc-thread) can float just above it.
  // RAF-coalesced to avoid layout thrash when the textarea resizes on
  // every keystroke.
  const footerRef = useCallback((el: HTMLDivElement | null) => {
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
  }, []);

  const bookmarkContextValue = useMemo(() => ({
    bookmarkedIds: bookmarkedMessageIds ?? EMPTY_BOOKMARK_IDS,
    onToggleBookmark: onToggleMessageBookmark,
  }), [bookmarkedMessageIds, onToggleMessageBookmark]);

  return (
    <div ref={threadRef} style={{ display: 'flex', flex: 1, minHeight: 0 }}>
      <MessageBookmarkContext.Provider value={bookmarkContextValue}>
      <ThreadPrimitive.Root className={`oc-thread${showToolDetails ? '' : ' oc-hide-tool-details'}`}>
        {/* Disable the library's built-in auto-scroll, which uses a
             1px at-bottom tolerance that races with streaming DOM growth and
             snaps the viewport down even when the user has scrolled up to
             read. useStickyBottom owns all auto-scroll with an 80px band that
             respects a deliberate scroll-up. */}
        <ThreadPrimitive.Viewport ref={setViewportRef} className="oc-thread-viewport" autoScroll={false}>
          {hasMore && loadingMore && (
            <div className="oc-load-more">
              <span className="oc-spinner" /> Loading older messages...
            </div>
          )}
          <ThreadPrimitive.Empty>
            <div className="oc-empty">No messages yet.</div>
          </ThreadPrimitive.Empty>
          <ThreadPrimitive.Messages components={THREAD_MESSAGE_COMPONENTS} />
          {footer && <div className="oc-thread-footer">{footer}</div>}
          <ThreadPrimitive.ViewportFooter ref={footerRef} className="oc-viewport-footer">
            {composer}
          </ThreadPrimitive.ViewportFooter>
        </ThreadPrimitive.Viewport>
        <ThreadPrimitive.ScrollToBottom className="oc-scroll-btn">
          Scroll to bottom
        </ThreadPrimitive.ScrollToBottom>
      </ThreadPrimitive.Root>
      </MessageBookmarkContext.Provider>
    </div>
  );
}
