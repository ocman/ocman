import { Fragment, useEffect, useState, type ComponentProps, type ReactNode } from 'react';
import { MessagePrimitive, ThreadPrimitive, useAuiState, type ThreadMessage, type ToolCallMessagePart } from '@assistant-ui/react';
import { parseA2UIResult } from '../../lib/a2ui';
import { formatDuration } from '../../lib/format';
import type { QuestionTiming } from '../../lib/convertToolPart';
import { isMutedTool } from '../../lib/mutedTools';
import type { TurnAggregate } from '../../lib/turnStats';
import { useUiStore } from '../../lib/uiStore';
import { useIsPrinting } from '../../lib/useIsPrinting';
import { usePrintCollapse } from '../../lib/printCollapseContext';
import { AssistantFailureNote, AssistantMeta } from './AssistantMeta';
import { ExplorationContent } from './ExplorationContent';
import { MarkdownText } from './MarkdownText';
import { TurnSummaryBar } from './TurnSummaryBar';
import { ActivitySliceContext, type ActivitySlice } from './agentActivityState';
import { hasAssistantError } from './useMessage';
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
      {inline ? <AssistantFailureNote /> : <AssistantMeta showFailure={false} />}
      {inline && !(message.metadata.custom?.turnStats as TurnAggregate | undefined)?.isLive && <TurnSummaryBar actions={actions} />}
    </MessagePrimitive.Root>
  </>;
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
  const label = `${live ? 'Working…' : 'Worked for'} ${formatDuration(live ? now - startedAt : duration ?? 0)}`;
  if (!children) return <div className="oc-tool-compact-line oc-agent-activity-label">{label}</div>;
  return <details className="oc-agent-activity" open={open || (printing && !printCollapse)}
    onToggle={(event) => { if (!printing) setOpen(event.currentTarget.open); }}>
    <summary className="oc-tool-compact-line oc-agent-activity-label">
      <i className={`bi bi-chevron-${open ? 'down' : 'right'} oc-read-arrow`} aria-hidden="true" />
      <span>{label}</span>
    </summary>
    <div className="oc-agent-activity-children">{children}</div>
  </details>;
}

type WorkBlock = { startedAt: number; live: boolean; duration: number | null; work: number[] };
type WorkBoundary = { index: number; startedAt: number; resumedAt?: number; question?: boolean };
type Turn = WorkBlock & { boundaries: WorkBoundary[] };

function questionBoundary(message: ThreadMessage, index: number): WorkBoundary | undefined {
  const questions = message.content.filter((part): part is ToolCallMessagePart => part.type === 'tool-call' && part.toolName === '__question__');
  if (!questions.length) return;
  type Timing = { questionTiming?: QuestionTiming };
  const first = questions[0].artifact as Timing | undefined;
  const last = questions[questions.length - 1].artifact as Timing | undefined;
  return { index, startedAt: first?.questionTiming?.startedAt ?? message.createdAt.getTime(), resumedAt: last?.questionTiming?.answeredAt, question: true };
}

function splitWork(turn: Turn, messages: readonly ThreadMessage[], afterInline: Map<number, WorkBlock>): WorkBlock {
  if (!turn.boundaries.length) return turn;
  let startedAt = turn.startedAt;
  let offset = 0;
  let first: WorkBlock | undefined;
  let previous: WorkBoundary | undefined;
  for (const boundary of turn.boundaries) {
    const start = offset;
    const limit = boundary.index + (boundary.question ? 1 : 0);
    while (offset < turn.work.length && turn.work[offset] < limit) offset++;
    const block = { startedAt, live: false, duration: boundary.startedAt - startedAt, work: turn.work.slice(start, offset) };
    if (previous) afterInline.set(previous.index, block);
    else first = block;
    previous = boundary;
    // With older history lacking answer timestamps, only count from the first recorded work.
    startedAt = boundary.resumedAt ?? messages[turn.work[offset]]?.createdAt.getTime() ?? boundary.startedAt;
  }
  const work = turn.work.slice(offset);
  if (previous && (work.length || previous.resumedAt !== undefined)) {
    const endedAt = turn.startedAt + (turn.duration ?? 0);
    afterInline.set(previous.index, { startedAt, live: turn.live, duration: endedAt - startedAt, work });
  }
  return first ?? turn;
}

type ActivityMessagesProps = { components: ThreadComponents; promptAliases?: ReadonlyMap<string, string> };

function CompactMessages({ components, promptAliases }: ActivityMessagesProps) {
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
    const turn = turns.get(turnId) ?? { startedAt, live: isLive, duration: wallClockMs, work: [], boundaries: [] };
    const parts = activityParts(message);
    if (parts.work.length || parts.text.work) turn.work.push(index);
    const question = questionBoundary(message, index);
    if (question) turn.boundaries.push(question);
    else if (custom?.activityInlineText && !hasAssistantError(message.status) && (isLive || !stats.isSummaryAnchor)) {
      const startedAt = message.createdAt.getTime();
      turn.boundaries.push({ index, startedAt, resumedAt: startedAt });
    }
    if (hasInlineContent(message, parts, stats)) inline.add(index);
    turns.set(turnId, turn);
  });
  if (latestPrompt && !turns.has(latestPrompt.id) && (running || latestPrompt.metadata.custom?.activityPending)) {
    turns.set(latestPrompt.id, { startedAt: latestPrompt.createdAt.getTime(), live: true, duration: null, work: [], boundaries: [] });
  }

  // A shell wake-up can start its own turn or arrive during an existing one.
  let currentTurnId: string | undefined;
  messages.forEach((message, index) => {
    const custom = message.metadata.custom;
    const stats = custom?.turnStats as TurnAggregate | undefined;
    if (stats?.turnId) currentTurnId = stats.turnId;
    if (custom?.activityPrompt || custom?.activityPending || turns.has(message.id)) currentTurnId = message.id;
    if (!custom?.activityShell || !currentTurnId) return;
    const turn = turns.get(currentTurnId);
    if (!turn) return;
    turn.work.push(index);
    inline.delete(index);
  });
  for (const turn of turns.values()) turn.work.sort((a, b) => a - b);

  // In a paginated window without its user prompt, start at the first visible reply.
  const afterInline = new Map<number, WorkBlock>();
  const anchors = new Map([...turns].map(([id, turn]) => [ids.has(id) ? id : firstReply.get(id), splitWork(turn, messages, afterInline)]));
  return messages.map((message, index) => {
    const alias = promptAliases?.get(message.id);
    // Props can pick up the receipt before assistant-ui drops the optimistic message.
    const awaitingRemoval = alias !== undefined && ids.has(alias);
    const turn = awaitingRemoval ? undefined : anchors.get(message.id);
    const row = <ThreadPrimitive.MessageByIndex index={index} components={components} />;
    const agent = message.role === 'assistant' && !!(message.metadata.custom?.turnStats as TurnAggregate | undefined)?.turnId;
    return <Fragment key={awaitingRemoval ? message.id : alias ?? message.id}>
      {!agent && inline.has(index) && row}
      <WorkDisclosure block={turn} messages={messages} components={components} />
      {agent && inline.has(index) && <ActivitySliceContext.Provider value="inline">{row}</ActivitySliceContext.Provider>}
      <WorkDisclosure block={afterInline.get(index)} messages={messages} components={components} />
    </Fragment>;
  });
}

function WorkDisclosure({ block, messages, components }: {
  block: WorkBlock | undefined; messages: readonly ThreadMessage[]; components: ThreadComponents;
}) {
  if (!block || (!block.live && !block.work.length)) return null;
  return <ActivityDetails startedAt={block.startedAt} live={block.live} duration={block.duration}>
    {block.work.length > 0 ? <ActivitySliceContext.Provider value="work">
      {block.work.map(index => <ThreadPrimitive.MessageByIndex key={messages[index].id} index={index} components={components} />)}
    </ActivitySliceContext.Provider> : null}
  </ActivityDetails>;
}

export function AgentActivityMessages({ components, promptAliases }: ActivityMessagesProps) {
  const collapse = useUiStore((s) => s.collapseAgentActivity);
  return collapse ? <CompactMessages components={components} promptAliases={promptAliases} /> : <ThreadPrimitive.Messages components={components} />;
}

function hasInlineContent(message: ThreadMessage, parts: ReturnType<typeof activityParts>, stats: TurnAggregate) {
  return hasAssistantError(message.status) || (!stats.isLive && stats.isSummaryAnchor) || parts.inline.length || parts.text.inline
    || message.metadata.custom?.timelineAt || message.metadata.custom?.modelChangedTo;
}
