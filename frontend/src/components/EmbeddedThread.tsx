/**
 * EmbeddedThread renders a compact, read-only preview of a sub-session's
 * conversation inside a Task tool card. It reuses the same
 * `convertMessages` pipeline as the main thread so tool calls, markdown,
 * and images render identically — just in a smaller, scrollable container.
 *
 * Unlike the main AssistantThread (which needs the full @assistant-ui/react
 * runtime), this component renders the converted ThreadMessageLike items
 * directly as plain React elements. It's read-only: no composer, no SSE.
 *
 * The container auto-scrolls to the bottom when new content arrives,
 * unless the user has manually scrolled up.
 */
import { useEffect, useMemo, useRef } from 'react';
import type { FC } from 'react';
import type { ThreadMessageLike } from '@assistant-ui/react';
import type { Message, Part } from '../lib/api';
import { convertMessages } from '../lib/convertMessages';
import { isMutedTool } from '../lib/mutedTools';
import { MarkdownContent } from './assistant/MarkdownText';

/** Render a single content item from a converted ThreadMessageLike. */
function ContentItem({ item }: { item: unknown }) {
  if (!item || typeof item !== 'object') return null;
  const it = item as Record<string, unknown>;

  if (it.type === 'text' && typeof it.text === 'string') {
    const text = it.text as string;
    if (!text.trim()) return null;
    return (
      <div className="oc-md">
        <MarkdownContent text={text} />
      </div>
    );
  }

  if (it.type === 'image' && typeof it.image === 'string') {
    return (
      <div className="oc-image-wrap">
        <img
          src={it.image as string}
          alt="attachment"
          className="oc-image"
          loading="lazy"
          style={{ maxWidth: 200, maxHeight: 150 }}
        />
      </div>
    );
  }

  if (it.type === 'tool-call') {
    const toolName = it.toolName as string;
    const argsText = (it.argsText as string) || '';
    const result = it.result as string | undefined;

    // Muted tools (reads, greps, etc.) render as a compact line.
    if (isMutedTool(toolName)) {
      // Strip the status line from argsText for muted tools.
      const label = argsText.split('\n').slice(1).join(' ').trim() || argsText;
      return (
        <div className="oc-read-line">
          <span className="oc-read-arrow">{'\u2192'}</span>
          <span>{label}</span>
        </div>
      );
    }

    // Generic tool call: show name + status, optionally the result.
    const lines = argsText.split('\n');
    const status = lines[0] || 'running';
    const label = lines.slice(1).join('\n').trim();

    let statusIcon = '\u2022';
    let statusClass = 'oc-tool-running';
    if (status === 'completed') { statusIcon = '\u2713'; statusClass = 'oc-tool-done'; }
    else if (status === 'error') { statusIcon = '\u2717'; statusClass = 'oc-tool-error'; }

    return (
      <div className={`oc-tool ${statusClass}`}>
        <div className="oc-tool-header">
          <span className={`oc-tool-icon ${statusClass}`}>{statusIcon}</span>
          <span className="oc-tool-label">{toolName}{label ? `: ${label.slice(0, 80)}` : ''}</span>
        </div>
        {result && (
          <div className="oc-tool-content">
            <div className="oc-tool-pre oc-tool-output">{result.slice(0, 500)}</div>
          </div>
        )}
      </div>
    );
  }

  return null;
}

/** Render a single converted message. */
function EmbeddedMessage({ msg }: { msg: ThreadMessageLike }) {
  const role = msg.role;
  const content = msg.content;

  // Normalise content to an array of items.
  let items: unknown[];
  if (typeof content === 'string') {
    items = content.trim() ? [{ type: 'text', text: content }] : [];
  } else if (Array.isArray(content)) {
    items = content;
  } else {
    return null;
  }

  // Skip empty messages.
  const hasContent = items.some((it) => {
    if (!it || typeof it !== 'object') return false;
    const o = it as Record<string, unknown>;
    if (o.type === 'text' && typeof o.text === 'string' && !(o.text as string).trim()) return false;
    return true;
  });
  if (!hasContent) return null;

  // Check if all items are muted tool calls — render as compact list.
  const allMuted = items.every((it) => {
    if (!it || typeof it !== 'object') return true;
    const o = it as Record<string, unknown>;
    if (o.type === 'text' && typeof o.text === 'string' && !(o.text as string).trim()) return true;
    if (o.type !== 'tool-call') return false;
    return isMutedTool(o.toolName as string);
  });

  const className = role === 'user'
    ? 'oc-msg oc-msg-user'
    : allMuted
      ? 'oc-msg oc-msg-muted'
      : 'oc-msg oc-msg-assistant';

  return (
    <div className={className}>
      <div className="oc-msg-body oc-md">
        {items.map((it, i) => <ContentItem key={i} item={it} />)}
      </div>
    </div>
  );
}

interface EmbeddedThreadProps {
  messages: Message[];
  parts: Part[];
}

/**
 * Compact, scrollable preview of a sub-session's conversation.
 * Renders the last N messages using the same visual language as the
 * main thread. Auto-scrolls to the bottom when new content arrives,
 * unless the user has scrolled up manually.
 */
export const EmbeddedThread: FC<EmbeddedThreadProps> = ({ messages, parts }) => {
  const converted = useMemo(
    () => convertMessages(messages, parts),
    [messages, parts],
  );

  const containerRef = useRef<HTMLDivElement>(null);
  // Track whether the user is "pinned" to the bottom. Starts true
  // so the initial render scrolls down.
  const isAtBottomRef = useRef(true);

  // On scroll, check if the user is near the bottom (within 30px).
  const handleScroll = () => {
    const el = containerRef.current;
    if (!el) return;
    const threshold = 30;
    isAtBottomRef.current =
      el.scrollHeight - el.scrollTop - el.clientHeight < threshold;
  };

  // Scroll to bottom when content changes, but only if the user
  // hasn't scrolled up.
  useEffect(() => {
    const el = containerRef.current;
    if (el && isAtBottomRef.current) {
      el.scrollTop = el.scrollHeight;
    }
  }, [converted]);

  if (converted.length === 0) return null;

  return (
    <div
      ref={containerRef}
      className="oc-embedded-thread"
      data-testid="embedded-thread"
      onScroll={handleScroll}
    >
      {converted.map((msg, i) => (
        <EmbeddedMessage key={msg.id || `msg-${i}`} msg={msg} />
      ))}
    </div>
  );
};
