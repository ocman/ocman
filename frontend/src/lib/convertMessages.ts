import type { ThreadMessageLike } from '@assistant-ui/react';
import type { Message, Part, PartData, TaskSessionData } from './api';
import type { FailedSend } from './failedSends';
import { messageModelRef } from './turnStats';
import { formatSeconds } from './format';
import type { ToolApproval } from './threadHelpers';
import {
  convertToolPart,
  convertUnknownPart,
  USER_TOOL_EXECUTION_NOTICE,
  type ImageItem,
  type ToolCallItem,
  type ToolPartContext,
} from './convertToolPart';

/**
 * Returns true when the MIME type denotes an image (`image/...`).
 * Used to decide whether a `file` part should render inline as an
 * image or as a downloadable attachment label.
 */
export function isImageMime(mime: string | undefined): boolean {
  return !!mime && mime.startsWith('image/');
}

/**
 * WeakMap cache for parsed part data. Keyed on the Part object
 * identity — when a part is updated immutably (new reference) the
 * old entry is automatically garbage-collected. Avoids re-running
 * `JSON.parse` on every SSE delta for parts that haven't changed.
 */
const parsedPartCache = new WeakMap<Part, PartData>();

function displayErrorMessage(message: string): string {
  try {
    const parsed = JSON.parse(message) as { error?: { message?: unknown } };
    return typeof parsed.error?.message === 'string' ? parsed.error.message : message;
  } catch {
    return message;
  }
}

/**
 * Parse a `Part`'s `data` field into a typed `PartData`. The result
 * is cached per Part instance, so identical part references skip
 * parsing on subsequent calls.
 */
export function parsePart(p: Part): PartData {
  const cached = parsedPartCache.get(p);
  if (cached !== undefined) return cached;
  let result: PartData;
  try {
    result = typeof p.data === 'string' ? JSON.parse(p.data) : p.data;
  } catch {
    result = (p.data || {}) as PartData;
  }
  parsedPartCache.set(p, result);
  return result;
}

/**
 * Truncate a string to at most `max` characters, appending a marker
 * with the original length so the reader knows truncation happened.
 * Returns the empty string for null/undefined/empty input.
 */
export function truncate(text: string | undefined | null, max: number): string {
  if (!text) return '';
  if (text.length <= max) return text;
  return text.slice(0, max) + '\n... (' + text.length + ' chars total)';
}

/**
 * Compute a path relative to the session's project directory.
 *
 * When the file lives under `projectDir`, returns the path with the
 * project prefix stripped (so reads display as `internal/db/foo.go`
 * instead of just `foo.go`). For files outside the project, returns
 * the full path so the reader can tell that the file lives outside
 * the checkout (e.g. `/etc/hosts`, `~/.config/foo`).
 */
export function relativizePath(absPath: string, projectDir: string): string {
  if (!absPath) return absPath;
  if (projectDir) {
    // Normalize the project directory by stripping a single trailing
    // slash, then check if absPath sits under it. Use `${dir}/` for
    // the prefix check so `/foo/bar` doesn't accidentally match
    // `/foo/barn`.
    const dir = projectDir.replace(/\/+$/, '');
    if (absPath === dir) return '.';
    const prefix = dir + '/';
    if (absPath.startsWith(prefix)) return absPath.slice(prefix.length);
  }
  return absPath;
}

/**
 * Determine if the session is actively running based on the last
 * message. The assistant is running if the last message has no
 * finish reason or completion timestamp (still streaming). Any finish
 * value, completion timestamp, or error means that turn is done.
 */
export function computeIsRunning(messages: Message[]): boolean {
  if (messages.length === 0) return false;
  const last = messages[messages.length - 1];
  if (!last.data) return false;
  if (last.data.role === 'user') return true;
  if (
    last.data.role === 'assistant' &&
    !last.data.finish &&
    !last.data.error &&
    last.data.time?.completed === undefined
  ) return true;
  return false;
}

/**
 * Per-message conversion cache. Stores the last conversion result
 * for each message, keyed on the message reference. The cache entry
 * also records the parts array reference and context values that
 * were used, so we can detect when a recomputation is needed.
 *
 * This avoids re-converting the entire thread on every SSE delta —
 * only the message whose parts changed gets recomputed.
 */
type ConvertedCacheEntry = {
  parts: Part[];
  pendingAgent: string | undefined;
  taskLiveOutput: Record<string, TaskSessionData> | undefined;
  projectDirectory: string | undefined;
  failedById: Record<string, FailedSend> | undefined;
  /** Resolved agent for this message (depends on neighbors). */
  msgAgent: string | undefined;
  /**
   * The `provider/model` this message switched the conversation to, when
   * it differs from the previously-active model. Empty when unchanged.
   * Depends on neighbors (the prior assistant model), so it participates
   * in the cache key like `msgAgent`.
   */
  modelChangedTo: string;
  /** Whether reasoning parts were rendered into this result (#290). */
  showReasoning: boolean;
  /** Current clock value when this message contains active reasoning. */
  reasoningNow: number;
  /** Signature of the AI approvals footnoted onto this message's tools. */
  approvalSig: string;
  result: ThreadMessageLike;
};
const convertedMessageCache = new WeakMap<Message, ConvertedCacheEntry>();

/** Stable empty array for messages with no parts. */
const EMPTY_PARTS: Part[] = [];

/**
 * Per-instance state owned by a `createConvertMessages()` closure.
 * Held outside the module so two simultaneously-mounted converters
 * (one per OcmanRuntimeProvider instance, which is one per session
 * detail page) don't share their result-array cache or their
 * `partsByMsg` index — a cross-session hit would smuggle the
 * previous session's array into the next session's snapshot.
 */
interface ConvertState {
  /** Last result array returned. Reused when the next call would
   *  produce an element-wise identical array, so the assistant-ui
   *  external store sees a stable snapshot reference. */
  lastResult: ThreadMessageLike[] | null;
  /** Last `parts` reference seen. When the new call passes the same
   *  reference we skip the `partsByMsg` rebuild. */
  lastPartsRef: Part[] | null;
  /** Memoised `partsByMsg` keyed on `lastPartsRef`. */
  lastPartsByMsg: Record<string, Part[]> | null;
}

/**
 * Returns true when an assistant message's parts indicate a non-LLM
 * operation that has already finished — e.g. a `!cmd` shell command
 * that OpenCode wraps in an assistant envelope without ever setting
 * `finish`. Mirrors the backend's `synthesizedTerminal` heuristic in
 * `internal/db/types.go`.
 *
 * Conditions (all must hold):
 *   1. The message has at least one part.
 *   2. None of the parts is a `step-start` (no LLM turn was initiated).
 *   3. None of the parts is in a `running` state (no tool still in flight).
 */
export function isSynthesizedTerminal(msgParts: Part[]): boolean {
  if (msgParts.length === 0) return false;
  for (const p of msgParts) {
    const pd = parsePart(p);
    if (pd.type === 'step-start') return false;
    const state = pd.state as Record<string, unknown> | undefined;
    if (state && state.status === 'running') return false;
  }
  return true;
}

/**
 * When a part was created, in unix ms. `timeCreated` comes from the DB
 * column and is therefore absent on live SSE part snapshots
 * (`reducePartSnapshot` builds the Part without it), so fall back to
 * the tool's own `time.start` — same fallback as `toolTimeSuffix`.
 * Returns 0 when neither is known.
 */
function partStartedAt(p: Part): number {
  return p.timeCreated || parsePart(p).time?.start || 0;
}

/** Permissions whose metadata only applies to the same named tool. */
const TOOL_SPECIFIC_PERMISSIONS = new Set([
  'bash', 'edit', 'write', 'read', 'webfetch', 'glob', 'grep', 'skill', 'task',
]);

function toolSpecificPermission(permission: string): string | undefined {
  const normalizedPermission = permission.toLowerCase().replace(/^mcp_/, '');
  for (const tool of TOOL_SPECIFIC_PERMISSIONS) {
    if (normalizedPermission === tool || normalizedPermission.startsWith(`${tool} `)) return tool;
  }
  return undefined;
}

function metadataLeaves(value: unknown, key = ''): Array<{ key: string; value: unknown }> {
  if (Array.isArray(value)) return value.flatMap((item) => metadataLeaves(item, key));
  if (value && typeof value === 'object') {
    return Object.entries(value as Record<string, unknown>)
      .flatMap(([childKey, child]) => metadataLeaves(child, childKey));
  }
  return value === undefined || value === null ? [] : [{ key, value }];
}

function canonicalMetadataKey(key: string): string {
  const normalized = key.toLowerCase().replaceAll('_', '');
  return normalized === 'file' || normalized === 'filepath' || normalized === 'path'
    ? 'path'
    : normalized;
}

function metadataMatchesInput(metadata: Record<string, unknown>, input: unknown): boolean {
  const expected = metadataLeaves(metadata);
  if (expected.length === 0) return false;
  const actual = metadataLeaves(input);
  const comparable = expected.filter((leaf) => actual.some((candidate) =>
    canonicalMetadataKey(candidate.key) === canonicalMetadataKey(leaf.key),
  ));
  return comparable.length > 0 && comparable.every((leaf) => actual.some((candidate) =>
    canonicalMetadataKey(candidate.key) === canonicalMetadataKey(leaf.key)
    && candidate.value === leaf.value,
  ));
}

function matchingToolPart(sortedToolParts: Part[], approval: PartData, noticeTime: number): Part | undefined {
  const askedAt = approval.askedAt || noticeTime;
  const metadata = approval.metadata;
  const permissionTool = toolSpecificPermission(approval.permission || '');
  for (let i = sortedToolParts.length - 1; i >= 0; i--) {
    const part = sortedToolParts[i];
    if (partStartedAt(part) > askedAt) continue;
    if (!metadata || Object.keys(metadata).length === 0) return part;
    const data = parsePart(part);
    const input = data.state?.input;
    const tool = (data.tool || '').toLowerCase().replace(/^mcp_/, '');
    if (permissionTool && tool !== permissionTool) continue;
    if (permissionTool === 'bash') {
      const command = metadataLeaves(metadata).find((leaf) => canonicalMetadataKey(leaf.key) === 'command')?.value;
      if (typeof command === 'string'
        && typeof input?.command === 'string'
        && input.command === command) return part;
      continue;
    }
    if (metadataMatchesInput(metadata, input)) return part;
  }
  return undefined;
}

/** Build (or rebuild) the `messageId → parts[]` index. */
function buildPartsByMsg(parts: Part[]): Record<string, Part[]> {
  const partsByMsg: Record<string, Part[]> = {};
  for (const p of parts) {
    if (!partsByMsg[p.messageId]) partsByMsg[p.messageId] = [];
    partsByMsg[p.messageId].push(p);
  }
  return partsByMsg;
}

/**
 * Shallow element-wise equality for Part arrays. Returns true when both
 * arrays have the same length and every element is the same reference.
 * This is needed because `partsByMsg` builds a fresh array on every
 * `convertMessages` call, so reference equality (`===`) always fails
 * even when the underlying Part objects haven't changed.
 */
function partsEqual(a: Part[], b: Part[]): boolean {
  if (a === b) return true;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

/**
 * Type of the function returned by `createConvertMessages()`. The
 * exported `convertMessages` matches this shape via a default
 * instance.
 */
/**
 * Permission approvals render on the tool call they unblocked. New
 * payloads match metadata against tool input; legacy payloads fall back
 * to the latest tool that started before the notice. Returns the
 * approvals keyed by tool part id plus the notice ids that were inlined
 * (and therefore drop out of the thread).
 */
function indexApprovals(
  messages: Message[],
  parts: Part[],
  partsByMsg: Record<string, Part[]>,
): { approvalsByPartId: Record<string, ToolApproval[]>; inlinedNotices: Set<string> } {
  const approvalsByPartId: Record<string, ToolApproval[]> = {};
  const inlinedNotices = new Set<string>();
  const notices = messages.filter((m) => m.data?.role === 'notice');
  if (notices.length === 0) return { approvalsByPartId, inlinedNotices };
  const toolParts = parts
    .filter((p) => parsePart(p).type === 'tool' && partStartedAt(p) > 0)
    .sort((a, b) => partStartedAt(a) - partStartedAt(b));
  for (const notice of notices) {
    for (const pd of (partsByMsg[notice.id] || EMPTY_PARTS).map(parsePart)) {
      if (pd.type !== 'auto-approved') continue;
      const target = matchingToolPart(toolParts, pd, notice.timeCreated);
      if (!target) continue;
      inlinedNotices.add(notice.id);
      const list = approvalsByPartId[target.id] || (approvalsByPartId[target.id] = []);
      list.push(approvalFromPart(pd));
    }
  }
  return { approvalsByPartId, inlinedNotices };
}

function approvalFromPart(pd: PartData): ToolApproval {
  return {
    permission: pd.permission || '',
    patterns: pd.patterns || [],
    reasoning: pd.reasoning || '',
    approvedBy: pd.approvedBy === 'user' ? 'user' : 'ai',
    reply: pd.reply,
    metadata: pd.metadata,
    askedAt: pd.askedAt,
    approvedAt: pd.approvedAt,
  };
}

/**
 * Detect mid-conversation model switches. Walk the messages in order
 * tracking the last user message; the first assistant message carrying
 * a model seeds the baseline (no chip), and any later assistant message
 * whose model differs from the previously-active one flags the user
 * message that triggered it so the renderer can draw a "model changed"
 * divider above that user turn. Falls back to the assistant message
 * when no preceding user message exists.
 */
function detectModelChanges(filtered: Message[]): Record<string, string> {
  const modelChangedById: Record<string, string> = {};
  let prevModel = '';
  let lastUserId = '';
  for (const m of filtered) {
    if (m.data?.role === 'user') lastUserId = m.id;
    if (m.data?.role !== 'assistant') continue;
    const ref = messageModelRef(m);
    if (!ref) continue;
    if (prevModel && ref !== prevModel) {
      modelChangedById[lastUserId || m.id] = ref;
    }
    prevModel = ref;
  }
  return modelChangedById;
}

/**
 * Synthetic notice messages (auto-approve, etc.) render as a special
 * assistant-role entry so they appear inline in the thread.
 */
function convertNoticeMessage(m: Message, rawParts: Part[]): ThreadMessageLike {
  const noticeContent: Exclude<ThreadMessageLike['content'], string>[number][] = [];
  for (const pd of rawParts.map(parsePart)) {
    if (pd.type === 'auto-approved') {
      noticeContent.push({
        type: 'tool-call' as const,
        toolCallId: m.id,
        toolName: 'ocman:auto-approved',
        argsText: '',
        artifact: { ocmanApprovals: [approvalFromPart(pd)] },
        result: undefined,
      });
    }
    if (pd.type === 'text' && pd.text) {
      noticeContent.push({ type: 'text' as const, text: pd.text });
    }
  }
  return {
    role: 'assistant' as const,
    id: m.id,
    content: noticeContent.length > 0 ? noticeContent : [{ type: 'text' as const, text: '' }],
    createdAt: new Date(m.timeCreated),
    status: { type: 'complete' as const, reason: 'stop' as const },
  };
}

export type ConvertMessagesFn = (
  messages: Message[],
  parts: Part[],
  pendingAgent?: string,
  taskLiveOutput?: Record<string, TaskSessionData>,
  projectDirectory?: string,
  failedById?: Record<string, FailedSend>,
  showReasoning?: boolean,
  now?: number,
) => ThreadMessageLike[];

/**
 * Build a per-instance `convertMessages` closure. Use this from
 * components so each consumer (one per session detail page) owns
 * its own result-array cache and `partsByMsg` memo. The shared
 * module-level WeakMap caches (`parsedPartCache`,
 * `convertedMessageCache`) are still used for cross-instance reuse
 * — they're keyed on the `Part` / `Message` identity so they're
 * safe to share across sessions.
 *
 * Returned function:
 *   - Filters to user/assistant messages only (system / tool roles
 *     are dropped).
 *   - Detects "queued" user messages — a user message that follows
 *     an unfinished assistant turn after the session already had a
 *     prior user turn — and surfaces them via `metadata.custom.queued`.
 *   - Resolves the responsible agent for each message and surfaces
 *     it via `metadata.custom.agent` so the renderer can colour
 *     bubbles consistently.
 *   - Special-cases tool calls (read/grep/glob/webfetch/edit/write/
 *     skill/task/question) into their compact rendering forms.
 *   - Returns the previous result-array reference when every element
 *     is unchanged (`useSyncExternalStore` snapshot stability).
 */
export function createConvertMessages(): ConvertMessagesFn {
  const state: ConvertState = {
    lastResult: null,
    lastPartsRef: null,
    lastPartsByMsg: null,
  };
  return function convert(
    messages: Message[],
    parts: Part[],
    pendingAgent?: string,
    taskLiveOutput?: Record<string, TaskSessionData>,
    projectDirectory?: string,
    failedById?: Record<string, FailedSend>,
    // Display-only: when false, assistant reasoning parts are dropped
    // from the rendered content (the `/thinking` toggle, #290). Defaults
    // to true so non-React callers and the default instance are unchanged.
    showReasoning: boolean = true,
    now: number = Date.now(),
  ): ThreadMessageLike[] {
    // Reuse the bucketed `partsByMsg` index when the input parts
    // array is the same reference we saw last time. Saves an O(N)
    // scan per call when SSE deltas leave parts identity stable.
    let partsByMsg: Record<string, Part[]>;
    if (state.lastPartsRef === parts && state.lastPartsByMsg) {
      partsByMsg = state.lastPartsByMsg;
    } else {
      partsByMsg = buildPartsByMsg(parts);
      state.lastPartsRef = parts;
      state.lastPartsByMsg = partsByMsg;
    }

    const { approvalsByPartId, inlinedNotices } = indexApprovals(messages, parts, partsByMsg);
    const hasApprovals = inlinedNotices.size > 0;

    const filtered = messages.filter(
      (m) => m.data?.role === 'user' || m.data?.role === 'assistant'
        || (m.data?.role === 'notice' && !inlinedNotices.has(m.id)),
    );

    const modelChangedById = detectModelChanges(filtered);

  const result = filtered.map((m, idx): ThreadMessageLike => {
    if (m.data?.role === 'notice') return convertNoticeMessage(m, partsByMsg[m.id] || EMPTY_PARTS);

    const role = m.data.role as 'user' | 'assistant';

    // Resolve the agent associated with this message so the UI can
    // colour it. For assistant messages the agent is on the message
    // itself. For user messages we attribute the colour to the agent
    // that replies — i.e. the next assistant message in the thread —
    // falling back to the currently-selected agent when the reply
    // hasn't been produced yet.
    let msgAgent: string | undefined;
    if (role === 'assistant') {
      msgAgent = m.data.agent || undefined;
    } else if (role === 'user') {
      for (let j = idx + 1; j < filtered.length; j++) {
        const later = filtered[j];
        if (later.data?.role === 'assistant' && later.data.agent) {
          msgAgent = later.data.agent;
          break;
        }
      }
      if (!msgAgent) msgAgent = pendingAgent || undefined;
    }

    // Per-message cache check: reuse the previous conversion result
    // when the message reference, its parts, and all context values
    // are unchanged. Parts are compared element-wise (same length +
    // same Part references) because `partsByMsg` builds a fresh array
    // on every call even when the underlying Part objects are stable.
    const modelChangedTo = modelChangedById[m.id] || '';
    const msgPartsRaw = partsByMsg[m.id] || EMPTY_PARTS;
    const reasoningNow = showReasoning && msgPartsRaw.some((part) => {
      const data = parsePart(part);
      return data.type === 'reasoning' && data.time?.start !== undefined && data.time.end === undefined;
    }) ? now : 0;
    // Approvals arrive after the tool part they annotate, so they must
    // participate in the cache key or the footnote never shows up.
    const approvalSig = hasApprovals
      ? msgPartsRaw
        .map((p) => (approvalsByPartId[p.id] ? `${p.id}:${JSON.stringify(approvalsByPartId[p.id])}` : ''))
        .filter(Boolean)
        .join(',')
      : '';
    const cached = convertedMessageCache.get(m);
    if (
      cached &&
      partsEqual(cached.parts, msgPartsRaw) &&
      cached.approvalSig === approvalSig &&
      cached.pendingAgent === pendingAgent &&
      cached.taskLiveOutput === taskLiveOutput &&
      cached.projectDirectory === projectDirectory &&
      cached.failedById === failedById &&
      cached.msgAgent === msgAgent &&
      cached.modelChangedTo === modelChangedTo &&
      cached.showReasoning === showReasoning &&
      cached.reasoningNow === reasoningNow
    ) {
      return cached.result;
    }

    const msgParts = msgPartsRaw.map(parsePart);

    // Build content as string | content array. Using string for
    // simple text, and the full content array format for messages
    // with tool calls or images.
    const textPieces: string[] = [];
    const imageParts: ImageItem[] = [];
    const toolCalls: ToolCallItem[] = [];

    const toolCtx: ToolPartContext = {
      messageId: m.id,
      msgParts,
      msgPartsRaw,
      msgCompleted: (m.data.time as { completed?: number } | undefined)?.completed || 0,
      projectDirectory: projectDirectory || '',
      taskLiveOutput,
      pendingUserToolExecutionNotice: false,
    };

    msgParts.forEach((pd, partIdx) => {
      // Skip non-renderable lifecycle parts
      if (pd.type === 'step-start' || pd.type === 'step-finish' || pd.type === 'snapshot') return;

      const toolCallsBefore = toolCalls.length;
      switch (pd.type) {
        case 'text':
          if (pd.text?.trim()) {
            if (pd.text.trim() === USER_TOOL_EXECUTION_NOTICE) {
              toolCtx.pendingUserToolExecutionNotice = true;
              break;
            }
            textPieces.push(pd.text);
          }
          break;
        case 'tool': {
          const { toolCall, images } = convertToolPart(toolCtx, pd, partIdx, toolCalls.length);
          toolCalls.push(toolCall);
          imageParts.push(...images);
          break;
        }
        case 'reasoning': {
          // Display-only toggle (#290): drop reasoning blocks entirely
          // when the user has hidden them via `/thinking`.
          if (showReasoning && pd.text?.trim()) {
            const { start, end } = pd.time ?? {};
            const duration = start !== undefined
              ? ` · ${formatSeconds(Math.max(0, (end ?? now) - start) / 1000)}`
              : '';
            textPieces.push(`> **${end !== undefined ? 'Thought' : 'Thinking'}:** ${pd.text}${duration}`);
          }
          break;
        }
        case 'patch': {
          const file = pd.file || pd.path || 'unknown file';
          const diff = pd.content || pd.diff || '';
          if (diff) {
            textPieces.push(`**${file}**\n\`\`\`diff\n${diff}\n\`\`\``);
          }
          break;
        }
        case 'file': {
          // Image / file parts from OpenCode — render images inline.
          if (isImageMime(pd.mime) && pd.url) {
            imageParts.push({ type: 'image' as const, image: pd.url });
          } else if (pd.url && pd.filename) {
            // Non-image file - show as a text link/label.
            textPieces.push(`📎 ${pd.filename} (${pd.mime || 'file'})`);
          }
          break;
        }
        default:
          // Unrecognized part types render as tool-like operations so
          // they still appear in the UI.
          toolCalls.push(convertUnknownPart(toolCtx, pd, partIdx, toolCalls.length));
          break;
      }

      // Footnote any approval that unblocked this part onto the
      // tool call(s) it produced.
      const approvals = approvalsByPartId[msgPartsRaw[partIdx]?.id || ''];
      if (approvals) {
        for (let i = toolCallsBefore; i < toolCalls.length; i++) {
          toolCalls[i].artifact = { ocmanApprovals: approvals };
        }
      }
    });

    // If the message has an error object, inject the error details
    // as visible text — but skip abort errors since the UI already
    // shows an "interrupted" indicator.
    if (role === 'assistant' && m.data.error) {
      const errName = m.data.error.name || 'Error';
      const isAbort = errName === 'MessageAbortedError' || errName === 'AbortError';
      if (!isAbort) {
        const errMessage = displayErrorMessage(m.data.error.data?.message || 'An unknown error occurred');
        textPieces.push(`**${errName}:** ${errMessage}`);
      }
    }

    // User messages cannot contain tool-call parts in assistant-ui.
    const visibleToolCalls = role === 'assistant' ? toolCalls : [];

    const synthesizedTerminal = role === 'assistant' && isSynthesizedTerminal(msgPartsRaw);
    const msgStatus = role === 'assistant'
      ? (m.data.finish === 'error' || m.data.error)
        ? { type: 'incomplete' as const, reason: 'error' as const }
        : m.data.finish || m.data.time?.completed !== undefined || synthesizedTerminal
          ? { type: 'complete' as const, reason: 'stop' as const }
          : { type: 'running' as const }
      : undefined;

    const failedEntry = role === 'user' ? failedById?.[m.id] : undefined;
    const model = role === 'assistant' ? messageModelRef(m) : '';
    const customMeta = {
      ...(m.data.tokens ? { tokens: m.data.tokens } : {}),
      ...(m.data.time ? { time: m.data.time } : {}),
      ...(m.data.error ? { errorName: m.data.error.name || 'Error' } : {}),
      ...(msgAgent ? { agent: msgAgent } : {}),
      ...(model ? { model } : {}),
      ...(modelChangedTo ? { modelChangedTo } : {}),
      ...(failedEntry ? { failed: { error: failedEntry.error, imagesDropped: !!failedEntry.imagesDropped } } : {}),
    };
    const metadata = Object.keys(customMeta).length > 0 ? { custom: customMeta } : undefined;

    // Build the final ThreadMessageLike result.
    let result: ThreadMessageLike;

    // If only text (no tool calls or images), use simple string content.
    if (visibleToolCalls.length === 0 && imageParts.length === 0) {
      result = {
        role,
        id: m.id,
        content: textPieces.join('\n\n') || '',
        createdAt: new Date(m.timeCreated),
        status: msgStatus,
        ...(metadata ? { metadata } : {}),
      };
    } else {
      // Mix of text, images, and tool calls.
      const content: ThreadMessageLike['content'] = [];
      if (textPieces.length > 0) {
        (content as Array<{ type: 'text'; text: string }>).push({ type: 'text', text: textPieces.join('\n\n') });
      }
      imageParts.forEach((img) => {
        (content as Array<unknown>).push(img);
      });
      visibleToolCalls.forEach((tc) => {
        (content as Array<unknown>).push(tc);
      });

      result = {
        role,
        id: m.id,
        content,
        createdAt: new Date(m.timeCreated),
        status: msgStatus,
        ...(metadata ? { metadata } : {}),
      };
    }

    // Store in per-message cache.
    convertedMessageCache.set(m, {
      parts: msgPartsRaw,
      pendingAgent,
      taskLiveOutput,
      projectDirectory,
      failedById,
      msgAgent,
      modelChangedTo,
      showReasoning,
      reasoningNow,
      approvalSig,
      result,
    });

    return result;
  });

    // Return the previous result array when every element is the same
    // reference. This prevents useSyncExternalStore (inside
    // @assistant-ui/react's useExternalStoreRuntime) from seeing a new
    // snapshot on every call, which would trigger a forceStoreRerender
    // loop during the passive-effect phase. Per-instance so two
    // simultaneously-mounted converters can't see each other's
    // arrays.
    const prev = state.lastResult;
    if (
      prev &&
      prev.length === result.length &&
      result.every((r, i) => r === prev[i])
    ) {
      return prev;
    }
    state.lastResult = result;
    return result;
  };
}

/**
 * Default-instance `convertMessages` for non-React callers and
 * legacy call sites. New code mounted inside a component should
 * use `createConvertMessages()` via `useMemo([sessionId])` so the
 * cache lifetime matches the component's lifetime.
 *
 * Two callers using this shared instance will compete for the
 * result-array cache slot — fine for tests, **not** safe to use
 * from two simultaneously-mounted components.
 */
export const convertMessages: ConvertMessagesFn = createConvertMessages();
