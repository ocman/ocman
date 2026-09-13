// Tool-part rendering for `convertMessages`. Turns one OpenCode tool
// part into the compact `tool-call` content item AssistantThread
// renders: reads/greps/globs collapse to a muted line, edits/writes
// carry a structured diff, tasks embed the sub-session, everything
// else is a status-prefixed block with the raw output.

import type { Part, PartData, FilePart, TaskSessionData } from './api';
import { extractTaskId } from './taskId';
import { SHELL_DESC_META, type ToolApproval } from './threadHelpers';
import { isImageMime, isSynthesizedTerminal, relativizePath, truncate } from './convertMessages';

export const USER_TOOL_EXECUTION_NOTICE = 'The following tool was executed by the user';
export const USER_EXECUTED_TOOL_META = '@user-executed-tool';

export interface ToolCallItem {
  type: 'tool-call';
  toolCallId: string;
  toolName: string;
  argsText: string;
  artifact?: { ocmanApprovals: ToolApproval[] };
  result?: string;
}

export interface ImageItem {
  type: 'image';
  image: string;
}

/** Per-message context shared by every tool part in that message. */
export interface ToolPartContext {
  messageId: string;
  msgParts: PartData[];
  msgPartsRaw: Part[];
  /** `message.time.completed`, or 0. */
  msgCompleted: number;
  projectDirectory: string;
  taskLiveOutput?: Record<string, TaskSessionData>;
  /**
   * Set when a "The following tool was executed by the user" text part
   * precedes a bash tool; consumed by the next bash tool call so it
   * renders with the user-executed marker.
   */
  pendingUserToolExecutionNotice: boolean;
}

type ToolState = NonNullable<PartData['state']>;

// Build a time-suffix string for a tool part. `startedAt` is the
// part's own timeCreated; `completedAt` is the next part's
// timeCreated (or the message's time.completed for the last tool).
function toolCompletedAt(ctx: ToolPartContext, partIdx: number): number {
  const currentEnd = ctx.msgParts[partIdx]?.time?.end || 0;
  if (currentEnd) return currentEnd;
  // Walk forward to find the next tool part's timeCreated.
  for (let j = partIdx + 1; j < ctx.msgPartsRaw.length; j++) {
    const nextPd = ctx.msgParts[j];
    const nextTime = ctx.msgPartsRaw[j].timeCreated || nextPd?.time?.start || 0;
    if (nextPd && nextPd.type === 'tool' && nextTime) {
      return nextTime;
    }
  }
  return ctx.msgCompleted;
}

function toolTimeSuffix(ctx: ToolPartContext, partIdx: number): string {
  const started = ctx.msgPartsRaw[partIdx]?.timeCreated || ctx.msgParts[partIdx]?.time?.start || 0;
  if (!started) return '';
  const ended = toolCompletedAt(ctx, partIdx);
  return `\n@time:${started},${ended || 0}`;
}

export function toolStatus(ctx: ToolPartContext, status: unknown, partIdx: number): string {
  if (typeof status === 'string' && status) return status;
  if (isSynthesizedTerminal(ctx.msgPartsRaw)) return 'completed';
  return toolCompletedAt(ctx, partIdx) ? 'completed' : 'running';
}

function isBash(toolName: string): boolean {
  return toolName === 'bash' || toolName === 'mcp_bash';
}

function shellUserExecutedSuffix(ctx: ToolPartContext, toolName: string, metadata: ToolState['metadata']): string {
  if (metadata?.ocmanUserExecutedShell && isBash(toolName)) {
    ctx.pendingUserToolExecutionNotice = false;
    return `\n${USER_EXECUTED_TOOL_META}`;
  }
  if (!ctx.pendingUserToolExecutionNotice) return '';
  ctx.pendingUserToolExecutionNotice = false;
  return isBash(toolName) ? `\n${USER_EXECUTED_TOOL_META}` : '';
}

// Shell tools keep their command verbatim in argsText: the description
// travels as a `@desc:` marker line, so a multi-line command (heredoc)
// never has its first line mistaken for a description. Titles that just
// restate the command are dropped.
function toolTitleEncoding(toolName: string, title: string, command: string): { meta: string; titleLine: string } {
  if (!isBash(toolName)) {
    return { meta: '', titleLine: title ? title + '\n' : '' };
  }
  // OpenCode titles a bash part with the command itself, newlines
  // collapsed to spaces — compare whitespace-insensitively so that
  // never shows up as a description.
  const flatten = (s: string) => s.replace(/\s+/g, ' ').trim();
  const desc = flatten(title);
  const redundant = !desc || flatten(command).startsWith(desc.replace(/…\s*$/, ''));
  return { meta: redundant ? '' : `\n${SHELL_DESC_META}${desc}`, titleLine: '' };
}

function toolOutput(st: ToolState): string {
  const output = st.output ?? st.metadata?.output ?? st.error;
  if (typeof output === 'string') return output;
  if (output != null) return JSON.stringify(output, null, 2);
  return '';
}

function defaultArgsText(input: unknown, inp: Record<string, string>, dropEmptyObject: boolean): string {
  if (typeof input === 'string') return input;
  if (inp.command) return inp.command;
  if (inp.filePath) return inp.filePath;
  if (!dropEmptyObject && inp.prompt) return inp.prompt;
  const s = JSON.stringify(input, null, 2);
  return dropEmptyObject && s === '{}' ? '' : s;
}

function diffResult(filePath: string, before: string, after: string): string {
  return JSON.stringify({ __diff: true, filePath, before, after });
}

/** Muted one-line tool call (`__read__` / `__skill__` renderers). */
function mutedLine(ctx: ToolPartContext, toolName: string, renderer: '__read__' | '__skill__', text: string, callIndex: number): ToolCallItem {
  return {
    type: 'tool-call',
    toolCallId: `${ctx.messageId}-${toolName}-${callIndex}`,
    toolName: renderer,
    argsText: text,
    result: undefined,
  };
}

/**
 * Tools with a bespoke compact rendering. Returns null when `toolName`
 * isn't one of them so the caller falls back to the generic block.
 */
function renderSpecialTool(
  ctx: ToolPartContext,
  toolName: string,
  st: ToolState,
  input: unknown,
  inp: Record<string, string>,
  argsText: string,
  title: string,
  partIdx: number,
  callIndex: number,
): ToolCallItem | null {
  switch (toolName) {
    case 'read':
    case 'mcp_read': {
      // Render reads as a muted inline line, not a collapsible block.
      // Paths are shown relative to the session's project directory.
      const readTarget = inp.filePath || argsText || title || 'file';
      const displayPath = relativizePath(readTarget, ctx.projectDirectory);
      const params: string[] = [];
      if (inp.offset) params.push(`offset=${inp.offset}`);
      if (inp.limit) params.push(`limit=${inp.limit}`);
      const suffix = params.length > 0 ? ` [${params.join(', ')}]` : '';
      return mutedLine(ctx, toolName, '__read__', `Read ${displayPath}${suffix}`, callIndex);
    }
    case 'Skill':
    case 'skill':
    case 'mcp_Skill':
    case 'mcp_skill': {
      // Skill loads collapse to a single muted line — the input is just
      // the skill name and the output is the whole skill body.
      const skillName = inp.name || inp.skill || title || 'unknown';
      return mutedLine(ctx, toolName, '__skill__', `Skill "${skillName}"`, callIndex);
    }
    case 'task':
    case 'mcp_task':
    case 'Task':
    case 'mcp_Task': {
      // Render subagent calls without the prompt, with a link to the session.
      const desc = inp.description || title || 'Subagent task';
      const agentType = inp.subagent_type || '';
      const label = agentType ? `${desc} (${agentType})` : desc;
      const taskId = extractTaskId(st);
      let taskOutput = '';
      const status = toolStatus(ctx, st.status, partIdx);
      if (typeof st.output === 'string' && st.output.trim()) {
        // Some platforms wrap the final output in <task_result> tags;
        // strip the OpenCode task_id line if present.
        taskOutput = truncate(st.output.replace(/task_id:\s*ses_[^\s)]+[^\n]*\n?/, '').trim(), 5000);
      }
      // Sub-session data for the embedded thread preview — available
      // while running (polling) and after completion (persisted).
      const subSession: TaskSessionData | undefined = taskId ? ctx.taskLiveOutput?.[taskId] : undefined;
      // Live tool list comes from the platform hook cache, injected by
      // the backend into state.metadata.liveTools for the running Task.
      type LiveTool = { toolName: string; summary?: string; subagentId?: string; startedAt?: string };
      let liveTools: LiveTool[] = [];
      if (status === 'running' && st.metadata) {
        const meta = st.metadata as Record<string, unknown>;
        if (Array.isArray(meta.liveTools)) {
          liveTools = (meta.liveTools as LiveTool[]).filter((t) => t && typeof t.toolName === 'string' && t.toolName !== '');
        }
      }
      return {
        type: 'tool-call',
        toolCallId: `${ctx.messageId}-${toolName}-${callIndex}`,
        toolName: '__task__',
        argsText: `${status}${toolTimeSuffix(ctx, partIdx)}\n${label}`,
        result: JSON.stringify({ taskId, taskOutput, subSession, liveTools }),
      };
    }
    case 'question':
    case 'mcp_question':
    case 'Question': {
      // Render questions as a special interactive-looking card.
      const questionsData = inp.questions || (input as { questions?: unknown } | undefined)?.questions;
      const questionsJson = questionsData
        ? (typeof questionsData === 'string' ? questionsData : JSON.stringify(questionsData))
        : JSON.stringify(input);
      return {
        type: 'tool-call',
        toolCallId: `${ctx.messageId}-${toolName}-${callIndex}`,
        toolName: '__question__',
        argsText: `${toolStatus(ctx, st.status, partIdx)}\n${questionsJson}`,
        result: typeof st.output === 'string' && st.output.trim()
          ? st.output
          : st.output ? JSON.stringify(st.output) : undefined,
      };
    }
    case 'grep':
    case 'mcp_grep': {
      const grepPattern = inp.pattern || argsText || title || '';
      const include = inp.include ? ` (${inp.include})` : '';
      return mutedLine(ctx, toolName, '__read__', `${grepPattern ? `Grep ${grepPattern}` : 'Grep'}${include}`, callIndex);
    }
    case 'glob':
    case 'mcp_glob': {
      const pattern = inp.pattern || argsText || title || '';
      const path = inp.path ? ` (${inp.path})` : '';
      return mutedLine(ctx, toolName, '__read__', `${pattern ? `Glob ${pattern}` : 'Glob'}${path}`, callIndex);
    }
    case 'webfetch':
    case 'mcp_webfetch':
    case 'mcp_Webfetch': {
      const url = inp.url || argsText || title || '';
      return mutedLine(ctx, toolName, '__read__', url ? `Fetch ${url}` : 'Webfetch', callIndex);
    }
    default:
      return null;
  }
}

function genericBlock(
  ctx: ToolPartContext,
  toolName: string,
  st: ToolState,
  title: string,
  argsText: string,
  resultText: string,
  partIdx: number,
  callIndex: number,
): ToolCallItem {
  const enc = toolTitleEncoding(toolName, title, argsText);
  return {
    type: 'tool-call',
    toolCallId: `${ctx.messageId}-${toolName}-${callIndex}`,
    toolName,
    argsText: `${toolStatus(ctx, st.status, partIdx)}${toolTimeSuffix(ctx, partIdx)}${shellUserExecutedSuffix(ctx, toolName, st.metadata)}${enc.meta}\n${enc.titleLine}${argsText}`,
    result: resultText || undefined,
  };
}

/** Convert a `type: 'tool'` part. */
export function convertToolPart(
  ctx: ToolPartContext,
  pd: PartData,
  partIdx: number,
  callIndex: number,
): { toolCall: ToolCallItem; images: ImageItem[] } {
  const st = pd.state || {};
  const input = st.input || {};
  const inp = input as Record<string, string>;
  let argsText = defaultArgsText(input, inp, false);
  let title = st.title || st.metadata?.description || inp.description || '';
  const toolName = pd.tool || 'unknown';

  const special = renderSpecialTool(ctx, toolName, st, input, inp, argsText, title, partIdx, callIndex);
  if (special) return { toolCall: special, images: [] };

  // For edit/write tools, generate a unified diff; the diff is shown as
  // the result so args are dropped. Pass a structured payload so
  // AssistantThread renders it with @pierre/diffs.
  let resultText = '';
  const isEdit = toolName === 'edit' || toolName === 'mcp_edit';
  const isWrite = toolName === 'write' || toolName === 'mcp_write' || toolName === 'mcp_Write';
  if (isWrite && inp.content) {
    title = 'Write ' + relativizePath(inp.filePath || title || 'file', ctx.projectDirectory);
    argsText = '';
    resultText = diffResult(inp.filePath || '', '', inp.content as string);
  } else if (isEdit && inp.oldString && inp.newString) {
    title = 'Edit ' + relativizePath(inp.filePath || title || 'file', ctx.projectDirectory);
    argsText = '';
    // Prefer full before/after from filediff metadata so the diff shows
    // real surrounding context.
    const fd = st.metadata?.filediff;
    resultText = fd && typeof fd.before === 'string' && typeof fd.after === 'string'
      ? diffResult(inp.filePath || '', fd.before, fd.after)
      : diffResult(inp.filePath || '', inp.oldString, inp.newString);
  } else {
    resultText = toolOutput(st);
  }

  const toolCall = genericBlock(ctx, toolName, st, title, argsText, resultText, partIdx, callIndex);

  // Extract image attachments from tool results (e.g. screenshot tools).
  const images: ImageItem[] = [];
  if (st.attachments && Array.isArray(st.attachments)) {
    for (const att of st.attachments as FilePart[]) {
      if (isImageMime(att.mime) && att.url) images.push({ type: 'image', image: att.url });
    }
  }
  return { toolCall, images };
}

/**
 * Convert a part of unrecognised type as a tool-like operation so it
 * still appears in the UI (e.g. "write", "file", custom tools).
 */
export function convertUnknownPart(ctx: ToolPartContext, pd: PartData, partIdx: number, callIndex: number): ToolCallItem {
  const st = pd.state || {};
  const input = st.input || {};
  const inp = input as Record<string, string>;
  const toolName = pd.tool || pd.type || 'unknown';
  let title = st.title || st.metadata?.description || inp.description || '';
  if (!title && inp.filePath) {
    title = toolName + ' ' + (inp.filePath.split('/').pop() || inp.filePath);
  }
  const argsText = defaultArgsText(input, inp, true);
  return genericBlock(ctx, toolName, st, title, argsText, toolOutput(st), partIdx, callIndex);
}
