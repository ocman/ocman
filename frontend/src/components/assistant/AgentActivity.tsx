import { Fragment, useEffect, useState, type ComponentProps, type ReactNode } from 'react';
import { MessagePrimitive, ThreadPrimitive, useAuiState, type ThreadMessage } from '@assistant-ui/react';
import { parseA2UIResult } from '../../lib/a2ui';
import { isMutedTool } from '../../lib/mutedTools';
import type { TurnAggregate } from '../../lib/turnStats';
import { useUiStore } from '../../lib/uiStore';
import { useIsPrinting } from '../../lib/useIsPrinting';
import { usePrintCollapse } from '../../lib/printCollapseContext';
import { AssistantMeta } from './AssistantMeta';
import { ExplorationContent } from './ExplorationContent';
import { MarkdownText } from './MarkdownText';
import { TurnSummaryBar } from './TurnSummaryBar';
import { ActivitySliceContext, type ActivitySlice } from './agentActivityState';
import './AgentActivity.css';

type ThreadComponents = NonNullable<ComponentProps<typeof ThreadPrimitive.Messages>['components']>;
type PartComponents = ComponentProps<typeof MessagePrimitive.Content>['components'];

function activityParts(message: ThreadMessage) {
  const inlineText = message.metadata.custom?.activityInlineText as string | undefined;
  const workText = message.metadata.custom?.activityWorkText as string | undefined;
  const text = { inline: inlineText ?? '', work: workText ?? '' };
  const inline: number[] = [];
  const work: number[] = [];
  message.content.forEach((part, index) => {
    if (part.type === 'text') {
      if (inlineText === undefined && part.text.trim()) work.push(index);
    } else if (part.type === 'tool-call' && (part.toolName === '__question__'
      || parseA2UIResult(part.toolName, part.argsText, part.result))) {
      inline.push(index);
    } else {
      work.push(index);
    }
  });
  return { inline, work, text };
}

export function ActivityMessage({ slice, components, decorations, actions }: {
  slice: ActivitySlice;
  components: PartComponents;
  decorations: ReactNode;
  actions: ReactNode;
}) {
  const message = useAuiState((s) => s.message);
  const parts = activityParts(message);
  const indices = parts[slice];
  const inline = slice === 'inline';
  const wholeWork = !inline && !parts.inline.length && !parts.text.inline;
  const toolsOnly = !parts.text[slice] && indices.length > 0 && indices.every((index) => message.content[index].type === 'tool-call');
  const muted = toolsOnly && indices.every((index) => {
    const part = message.content[index];
    return part.type === 'tool-call' && isMutedTool(part.toolName);
  });
  return <>
    {inline && decorations}
    <MessagePrimitive.Root className={`oc-msg ${muted ? 'oc-msg-muted' : `oc-msg-assistant${toolsOnly ? ' oc-msg-tools' : ''}`}`}
      {...(inline ? { 'data-message-id': message.id } : { 'data-activity-message-id': message.id })}>
      <div className="oc-msg-body oc-md">
        {wholeWork ? <ExplorationContent components={components} /> : <>
          {parts.text[slice] && <MarkdownText text={parts.text[slice]} />}
          {indices.map((index) => <MessagePrimitive.PartByIndex key={index} index={index} components={components} />)}
        </>}
      </div>
      {!inline && <AssistantMeta />}
      {inline && !(message.metadata.custom?.turnStats as TurnAggregate | undefined)?.isLive && <TurnSummaryBar actions={actions} />}
    </MessagePrimitive.Root>
  </>;
}

function formatElapsed(ms: number): string {
  const seconds = Math.floor(Math.max(0, ms) / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}

function ActivityDetails({ startedAt, live, duration, children }: {
  startedAt: number; live: boolean; duration: number | null; children: ReactNode;
}) {
  const [now, setNow] = useState(Date.now);
  const [open, setOpen] = useState(false);
  const printing = useIsPrinting();
  const printCollapse = usePrintCollapse();
  useEffect(() => {
    if (!live) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [live]);
  return <details className="oc-agent-activity" open={open || (printing && !printCollapse)}
    onToggle={(event) => { if (!printing) setOpen(event.currentTarget.open); }}>
    <summary>{live ? 'Working…' : 'Worked for'} {formatElapsed(live ? now - startedAt : duration ?? 0)}</summary>
    <div className="oc-agent-activity-children">{children}</div>
  </details>;
}

type Turn = { startedAt: number; live: boolean; duration: number | null; work: number[] };

function CompactMessages({ components }: { components: ThreadComponents }) {
  const messages = useAuiState((s) => s.thread.messages);
  const running = useAuiState((s) => s.thread.isRunning);
  const turns = new Map<string, Turn>();
  const ids = new Set(messages.map((message) => message.id));
  const inline = new Set<number>();
  const firstReply = new Map<string, string>();
  let latestPrompt: ThreadMessage | undefined;

  messages.forEach((message, index) => {
    const custom = message.metadata.custom;
    if (message.role === 'user' && (custom?.activityPrompt || custom?.activityPending) && !custom.failed) latestPrompt = message;
    const stats = custom?.turnStats as TurnAggregate | undefined;
    if (!stats?.turnId) {
      inline.add(index);
      return;
    }
    const { turnId, startedAt, isLive, wallClockMs } = stats;
    if (!firstReply.has(turnId)) firstReply.set(turnId, message.id);
    const turn = turns.get(turnId) ?? { startedAt, live: isLive, duration: wallClockMs, work: [] };
    const parts = activityParts(message);
    if (parts.work.length || parts.text.work) turn.work.push(index);
    if (hasInlineContent(message, parts, stats)) inline.add(index);
    turns.set(turnId, turn);
  });
  if (latestPrompt && !turns.has(latestPrompt.id) && (running || latestPrompt.metadata.custom?.activityPending)) {
    turns.set(latestPrompt.id, { startedAt: latestPrompt.createdAt.getTime(), live: true, duration: null, work: [] });
  }

  // In a paginated window without its user prompt, start at the first visible reply.
  const anchors = new Map([...turns].map(([id, turn]) => [ids.has(id) ? id : firstReply.get(id), turn]));
  return messages.map((message, index) => {
    const turn = anchors.get(message.id);
    const row = <ThreadPrimitive.MessageByIndex index={index} components={components} />;
    const agent = message.role === 'assistant' && !!(message.metadata.custom?.turnStats as TurnAggregate | undefined)?.turnId;
    return <Fragment key={message.id}>
      {!agent && inline.has(index) && row}
      {turn && <ActivityDetails startedAt={turn.startedAt} live={turn.live} duration={turn.duration}>
        <ActivitySliceContext.Provider value="work">
          {turn.work.length ? turn.work.map((workIndex) => <ThreadPrimitive.MessageByIndex key={messages[workIndex].id} index={workIndex} components={components} />)
            : <span>{turn.live ? 'No activity yet.' : 'No work details for this turn.'}</span>}
        </ActivitySliceContext.Provider>
      </ActivityDetails>}
      {agent && inline.has(index) && <ActivitySliceContext.Provider value="inline">{row}</ActivitySliceContext.Provider>}
    </Fragment>;
  });
}

export function AgentActivityMessages({ components }: { components: ThreadComponents }) {
  const collapse = useUiStore((s) => s.collapseAgentActivity);
  return collapse ? <CompactMessages components={components} /> : <ThreadPrimitive.Messages components={components} />;
}

function hasInlineContent(message: ThreadMessage, parts: ReturnType<typeof activityParts>, stats: TurnAggregate) {
  return (!stats.isLive && stats.isSummaryAnchor) || parts.inline.length || parts.text.inline
    || message.metadata.custom?.timelineAt || message.metadata.custom?.modelChangedTo;
}
