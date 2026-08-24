import hljs from 'highlight.js/lib/common';

/**
 * File-extension to highlight.js language mapping for diff and code
 * blocks rendered in the assistant thread. Extensions not in this
 * map fall through to highlight.js's auto-detection (which is good
 * enough for most prose languages but unreliable on tiny snippets).
 */
export const EXTENSION_LANGUAGE_MAP: Record<string, string> = {
  c: 'c',
  cc: 'cpp',
  cpp: 'cpp',
  css: 'css',
  cts: 'typescript',
  go: 'go',
  h: 'c',
  hpp: 'cpp',
  html: 'xml',
  htm: 'xml',
  java: 'java',
  js: 'javascript',
  json: 'json',
  jsx: 'javascript',
  mjs: 'javascript',
  mts: 'typescript',
  py: 'python',
  rb: 'ruby',
  rs: 'rust',
  sh: 'bash',
  sql: 'sql',
  toml: 'ini',
  ts: 'typescript',
  tsx: 'typescript',
  xml: 'xml',
  yml: 'yaml',
  yaml: 'yaml',
  zsh: 'bash',
};

/** Single section in a parsed `*** … File:` patch payload. */
export interface PatchSection {
  action: 'add' | 'update' | 'delete';
  path: string;
}

export interface ApplyPatchFileDiff {
  action: 'add' | 'update' | 'delete' | 'rename';
  path: string;
  oldPath?: string;
  patch: string;
}

/**
 * Result of `extractPatchPayload`: `patchText` is the raw patch body
 * when a structured payload was detected, otherwise `null`. The
 * `preamble` is whatever sat before the JSON envelope.
 */
export interface PatchPayload {
  patchText: string | null;
  preamble: string;
}

/** A single MCP-style question with its prompt header and option list. */
export interface QuestionOption {
  label: string;
  description: string;
}

export interface QuestionData {
  header: string;
  question: string;
  options: QuestionOption[];
}

/**
 * Escape the five HTML-significant characters so a string can be
 * safely interpolated into `dangerouslySetInnerHTML` markup.
 */
export function escapeHtml(text: string): string {
  return text
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

/**
 * Resolve a syntax-highlighting language from a path or basename.
 * `Dockerfile` matches by name; everything else looks up the file
 * extension in `EXTENSION_LANGUAGE_MAP`. Returns `undefined` when
 * no mapping is known.
 */
export function inferLanguageFromPath(path: string): string | undefined {
  const name = path.trim().split(/[\\/]/).pop() || path.trim();
  if (!name) return undefined;

  if (name === 'Dockerfile') return 'dockerfile';

  const extMatch = name.match(/\.([a-zA-Z0-9]+)$/);
  if (!extMatch) return undefined;

  const ext = extMatch[1].toLowerCase();
  return EXTENSION_LANGUAGE_MAP[ext];
}

/**
 * Pick a language for a diff block. Tries to derive it from the
 * `Edit <path>` or `Write <path>` title first, falling back to the
 * first line of the diff body (which often carries the path on
 * tools that omit a title).
 */
export function inferDiffLanguage(title: string, detail: string): string | undefined {
  const trimmedTitle = title.trim();
  const prefixed = trimmedTitle.match(/^(?:Edit|Write)\s+(.+)$/);
  const fromTitle = prefixed?.[1] || trimmedTitle;

  const fromTitleLang = inferLanguageFromPath(fromTitle);
  if (fromTitleLang) return fromTitleLang;

  const firstDetailLine = detail.split('\n')[0]?.trim() || '';
  return inferLanguageFromPath(firstDetailLine);
}

/**
 * Run highlight.js on a snippet, preferring the explicit
 * `languageHint` when present and falling back to autodetect. Errors
 * during highlighting fall back to HTML-escaped raw text so we never
 * inject unhighlighted markup with unescaped angle brackets.
 */
export function highlightDiffCode(code: string, languageHint?: string): string {
  if (!code) return '';

  try {
    if (languageHint && hljs.getLanguage(languageHint)) {
      return hljs.highlight(code, { language: languageHint, ignoreIllegals: true }).value;
    }
    return hljs.highlightAuto(code).value;
  } catch {
    return escapeHtml(code);
  }
}

/**
 * Parse a string that should be a single JSON object (`{...}`).
 * Returns `null` for arrays, primitives, malformed JSON, or strings
 * with content outside of the outermost braces.
 */
export function parseJsonObject(text: string): Record<string, unknown> | null {
  const trimmed = text.trim();
  if (!trimmed.startsWith('{') || !trimmed.endsWith('}')) return null;
  try {
    const parsed = JSON.parse(trimmed);
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : null;
  } catch {
    return null;
  }
}

/**
 * Like `parseJsonObject`, but tolerates leading / trailing prose
 * around the JSON object (e.g. tools that emit `Some preamble. {…}`).
 * Returns the inner object if the slice between the first `{` and
 * the last `}` parses cleanly.
 */
export function parseJsonObjectFromMixedText(text: string): Record<string, unknown> | null {
  const start = text.indexOf('{');
  const end = text.lastIndexOf('}');
  if (start < 0 || end <= start) return null;
  return parseJsonObject(text.slice(start, end + 1));
}

/**
 * Pull the `patchText` field out of a tool-call payload that may be
 * pure JSON or JSON wrapped in prose. When no patch is found, the
 * full text is returned as the preamble so the caller can render it
 * as plain text.
 */
export function extractPatchPayload(text: string): PatchPayload {
  const parsed = parseJsonObject(text) || parseJsonObjectFromMixedText(text);
  if (typeof parsed?.patchText === 'string') {
    const jsonStart = text.indexOf('{');
    const preamble = jsonStart > 0 ? text.slice(0, jsonStart).trim() : '';
    return { patchText: parsed.patchText, preamble };
  }

  const patchStart = text.indexOf('*** Begin Patch');
  if (patchStart >= 0) {
    return {
      patchText: text.slice(patchStart).trim(),
      preamble: text.slice(0, patchStart).trim(),
    };
  }

  return { patchText: null, preamble: text.trim() };
}

/**
 * Split a tool's raw arguments into a single-line title and the
 * remainder. The title is what we render at the top of the tool
 * card; the detail goes inside the body. `apply_patch` is special-
 * cased because its first line is part of the JSON payload, not a
 * label.
 */
export function splitToolArgs(toolName: string, rawArgs: string): { title: string; detail: string } {
  const argLines = rawArgs.split('\n');
  const firstLine = argLines[0] || '';
  const rest = argLines.slice(1).join('\n').trim();

  const trimmed = rawArgs.trim();
  if (
    (trimmed.startsWith('{') && trimmed.endsWith('}')) ||
    (trimmed.startsWith('[') && trimmed.endsWith(']'))
  ) {
    return { title: '', detail: trimmed };
  }

  if (toolName === 'apply_patch' && (firstLine.trim().startsWith('{') || !rest)) {
    return { title: '', detail: rawArgs.trim() };
  }

  return { title: firstLine, detail: rest };
}

/**
 * Priority list of argument keys that are most useful as a one-line
 * label for a generic / MCP tool call. The first key found in the
 * args wins. Order matters: things like `target` and `name` are more
 * identifying than `repo` or `branch`.
 */
const SUMMARY_PRIORITY_KEYS: ReadonlyArray<string> = [
  'target',
  'name',
  'route',
  'symbol_name',
  'symbolName',
  'filePath',
  'file_path',
  'file',
  'path',
  'query',
  'goal',
  'intent',
  'command',
  'url',
  'branch',
  'repo',
  'tool',
];

const SUMMARY_VALUE_MAX = 60;

/**
 * Render a single scalar value for the summary line. Strings are
 * quoted (e.g. `target="x"`), numbers and booleans are not
 * (`depth=4`). Returns null for non-scalar / empty values so callers
 * can keep searching.
 */
function formatSummaryEntry(key: string, value: unknown): string | null {
  if (value === null || value === undefined) return null;
  if (typeof value === 'string') {
    const trimmed = value.replace(/\s+/g, ' ').trim();
    if (!trimmed) return null;
    const truncated = trimmed.length > SUMMARY_VALUE_MAX
      ? `${trimmed.slice(0, SUMMARY_VALUE_MAX - 1)}\u2026`
      : trimmed;
    return `${key}="${truncated}"`;
  }
  if (typeof value === 'number' || typeof value === 'boolean') {
    return `${key}=${String(value)}`;
  }
  return null;
}

/**
 * Build a compact one-line summary for a tool call's arguments. Used
 * by the muted-line renderer for MCP and other generic tools so we
 * don't dump full JSON payloads into the conversation UI.
 *
 * Heuristic: if the args are JSON-shaped, look for a value under one
 * of `SUMMARY_PRIORITY_KEYS`; fall back to the first short scalar
 * field. If the args are plain text, return the first line. Returns
 * an empty string when nothing useful can be extracted (the caller
 * should fall back to rendering the bare tool name).
 */
export function summarizeToolArgs(rawArgs: string): string {
  const trimmed = (rawArgs || '').trim();
  if (!trimmed) return '';

  const looksLikeJson =
    (trimmed.startsWith('{') && trimmed.endsWith('}')) ||
    (trimmed.startsWith('[') && trimmed.endsWith(']'));

  if (looksLikeJson) {
    let parsed: unknown;
    try {
      parsed = JSON.parse(trimmed);
    } catch {
      return '';
    }

    if (Array.isArray(parsed)) {
      return parsed.length === 1 ? `[1 item]` : `[${parsed.length} items]`;
    }
    if (!parsed || typeof parsed !== 'object') return '';

    const obj = parsed as Record<string, unknown>;

    for (const key of SUMMARY_PRIORITY_KEYS) {
      if (key in obj) {
        const formatted = formatSummaryEntry(key, obj[key]);
        if (formatted !== null) return formatted;
      }
    }

    // Fallback: first scalar entry in iteration order.
    for (const [key, value] of Object.entries(obj)) {
      const formatted = formatSummaryEntry(key, value);
      if (formatted !== null) return formatted;
    }

    return '';
  }

  // Plain-text args: first non-empty line, truncated.
  const firstLine = trimmed.split('\n').find((line) => line.trim()) || '';
  const compact = firstLine.replace(/\s+/g, ' ').trim();
  if (!compact) return '';
  return compact.length > SUMMARY_VALUE_MAX
    ? `${compact.slice(0, SUMMARY_VALUE_MAX - 1)}\u2026`
    : compact;
}

/**
 * Walk a `*** … File: <path>` patch and return one entry per file
 * touched. Order matches the order of the corresponding header lines.
 */
export function parsePatchSections(patchText: string): PatchSection[] {
  return patchText.split('\n').flatMap<PatchSection>((line) => {
    const updateMatch = line.match(/^\*\*\* Update File: (.+)$/);
    if (updateMatch) return [{ action: 'update' as const, path: updateMatch[1] }];
    const addMatch = line.match(/^\*\*\* Add File: (.+)$/);
    if (addMatch) return [{ action: 'add' as const, path: addMatch[1] }];
    const deleteMatch = line.match(/^\*\*\* Delete File: (.+)$/);
    if (deleteMatch) return [{ action: 'delete' as const, path: deleteMatch[1] }];
    return [];
  });
}

interface PatchSectionWithLines {
  action: ApplyPatchFileDiff['action'];
  lines: string[];
  oldPath?: string;
  path: string;
}

function parsePatchSectionsWithLines(patchText: string): PatchSectionWithLines[] {
  const sections: PatchSectionWithLines[] = [];
  let current: PatchSectionWithLines | null = null;

  const pushCurrent = () => {
    if (current) sections.push(current);
  };

  for (const line of patchText.split('\n')) {
    const markerLine = line.trimStart();

    if (markerLine === '*** Begin Patch') continue;
    if (markerLine === '*** End Patch') {
      pushCurrent();
      current = null;
      continue;
    }

    const headerMatch = markerLine.match(/^\*\*\* (Add|Update|Delete) File: (.+)$/);
    if (headerMatch) {
      pushCurrent();
      const action = headerMatch[1] === 'Add'
        ? 'add'
        : headerMatch[1] === 'Delete'
          ? 'delete'
          : 'update';
      current = { action, path: headerMatch[2], lines: [] };
      continue;
    }

    if (!current) continue;
    const moveMatch = markerLine.match(/^\*\*\* Move to: (.+)$/);
    if (moveMatch) {
      current.oldPath = current.path;
      current.action = 'rename';
      current.path = moveMatch[1];
      continue;
    }
    current.lines.push(line);
  }

  pushCurrent();
  return sections;
}

function unifiedRange(start: number, count: number): string {
  if (count === 0) return `${start},0`;
  if (count === 1) return `${start}`;
  return `${start},${count}`;
}

/**
 * Convert apply_patch's envelope format into a synthetic unified diff.
 * The patch format only contains changed hunks, not full file contents,
 * so line numbers are snippet-relative; that is enough for the diff
 * renderer to display clean multi-file sections without raw markers.
 */
export function applyPatchToUnifiedFileDiffs(patchText: string): ApplyPatchFileDiff[] {
  const sections = parsePatchSectionsWithLines(patchText);
  if (sections.length === 0) return [];

  return sections.map((section) => {
    let oldCount = 0;
    let newCount = 0;
    const hunkLines: string[] = [];

    for (const rawLine of section.lines) {
      if (rawLine.startsWith('@@')) continue;
      if (rawLine.startsWith('\\ No newline at end of file')) {
        hunkLines.push(rawLine);
        continue;
      }

      if (rawLine.startsWith('+')) {
        newCount += 1;
        hunkLines.push(rawLine);
      } else if (rawLine.startsWith('-')) {
        oldCount += 1;
        hunkLines.push(rawLine);
      } else {
        oldCount += 1;
        newCount += 1;
        hunkLines.push(rawLine.startsWith(' ') ? rawLine : ` ${rawLine}`);
      }
    }

    const diffOldPath = section.oldPath ?? section.path;
    const oldPath = section.action === 'add' ? '/dev/null' : `a/${diffOldPath}`;
    const newPath = section.action === 'delete' ? '/dev/null' : `b/${section.path}`;
    const oldStart = oldCount === 0 ? 0 : 1;
    const newStart = newCount === 0 ? 0 : 1;

    const patch = [
      `diff --git a/${diffOldPath} b/${section.path}`,
      `--- ${oldPath}`,
      `+++ ${newPath}`,
      `@@ -${unifiedRange(oldStart, oldCount)} +${unifiedRange(newStart, newCount)} @@`,
      ...hunkLines,
    ].join('\n');

    return {
      action: section.action,
      path: section.path,
      oldPath: section.oldPath,
      patch,
    };
  });
}

export function applyPatchToUnifiedDiffs(patchText: string): string[] {
  return applyPatchToUnifiedFileDiffs(patchText).map((file) => file.patch);
}

export function applyPatchToUnifiedDiff(patchText: string): string | null {
  const files = applyPatchToUnifiedDiffs(patchText);
  if (files.length === 0) return null;
  return files.join('\n');
}

/**
 * Trim absolute paths down to a project-relative form so they fit on
 * a single tool-card line. Recognised prefixes (`/frontend/`,
 * `/internal/`, `/docs/`, `/.github/`) keep everything from the
 * marker onward; anything else falls back to the trailing two
 * segments. Relative paths pass through unchanged.
 */
export function shortenPatchPath(path: string): string {
  if (!path.startsWith('/')) return path;

  const markers = ['/frontend/', '/internal/', '/docs/', '/.github/'];
  for (const marker of markers) {
    const index = path.indexOf(marker);
    if (index >= 0) return path.slice(index + 1);
  }

  const parts = path.split('/').filter(Boolean);
  return parts.slice(-2).join('/');
}

/**
 * One-line patch summary for the tool-card title. A patch touching a
 * single file shows the action verb and shortened path; multi-file
 * patches show the file count and a per-action breakdown.
 */
export function summarizePatch(patchText: string): string {
  const sections = parsePatchSections(patchText);
  if (sections.length === 0) return 'Patch';
  if (sections.length === 1) {
    const section = sections[0];
    const action = section.action === 'add' ? 'Add' : section.action === 'delete' ? 'Delete' : 'Update';
    return `${action} ${shortenPatchPath(section.path)}`;
  }

  const counts = sections.reduce<{ add: number; update: number; delete: number }>(
    (acc, section) => {
      acc[section.action] += 1;
      return acc;
    },
    { add: 0, update: 0, delete: 0 },
  );
  const parts = [
    counts.add ? `${counts.add} add${counts.add === 1 ? '' : 's'}` : null,
    counts.update ? `${counts.update} update${counts.update === 1 ? '' : 's'}` : null,
    counts.delete ? `${counts.delete} delete${counts.delete === 1 ? '' : 's'}` : null,
  ].filter(Boolean);
  return `Patch ${sections.length} files${parts.length ? ` (${parts.join(', ')})` : ''}`;
}

/**
 * Best-effort decoder for the answer payload returned by the MCP
 * `question` tool. Handles:
 *
 *   - JSON arrays of strings, sub-arrays (joined), or objects with a
 *     `label` / `answer` / `value` / `text` field.
 *   - Double-encoded JSON (a JSON string that itself is JSON).
 *   - The prose form: `User has answered your questions: "Q1"="A1",
 *     "Q2"="A2". You can now …`.
 *   - Single-string answers wrapped in surrounding quotes.
 *
 * Returns the array of human-readable answer strings, or `null` when
 * nothing usable could be extracted.
 */
export function parseQuestionAnswers(result: unknown): string[] | null {
  if (typeof result !== 'string' || !result.trim()) return null;

  const normalizeAnswer = (value: string): string => {
    const trimmed = value.trim();
    const quotedMatch = trimmed.match(/^"([\s\S]+)"$/);
    if (quotedMatch) return quotedMatch[1].trim();
    return trimmed;
  };

  // Extract `"question"="answer"` pairs from the prose format. Bodies
  // may contain escaped quotes and newlines, so walk the string
  // matching balanced quoted segments separated by `=`.
  const extractProseAnswers = (raw: string): string[] | null => {
    const answers: string[] = [];
    let i = 0;
    const len = raw.length;
    const readQuoted = (): string | null => {
      if (i >= len || raw[i] !== '"') return null;
      i++; // skip opening quote
      let out = '';
      while (i < len) {
        const ch = raw[i];
        if (ch === '\\' && i + 1 < len) {
          out += raw[i + 1];
          i += 2;
          continue;
        }
        if (ch === '"') {
          i++; // skip closing quote
          return out;
        }
        out += ch;
        i++;
      }
      return null; // unterminated
    };

    while (i < len) {
      while (i < len && raw[i] !== '"') i++;
      if (i >= len) break;
      const q = readQuoted();
      if (q === null) break;
      while (i < len && (raw[i] === ' ' || raw[i] === '\t')) i++;
      if (raw[i] !== '=') continue;
      i++;
      while (i < len && (raw[i] === ' ' || raw[i] === '\t')) i++;
      const a = readQuoted();
      if (a === null) break;
      answers.push(a.trim());
    }
    return answers.length > 0 ? answers : null;
  };

  // The result may be JSON-stringified multiple times; unwrap up to
  // two levels.
  const unwrap = (raw: string): unknown => {
    try {
      const first = JSON.parse(raw);
      if (typeof first === 'string') {
        try { return JSON.parse(first); } catch { return first; }
      }
      return first;
    } catch {
      return raw;
    }
  };

  const parsed = unwrap(result);

  if (Array.isArray(parsed)) {
    const answers = parsed
      .map((entry) => {
        if (Array.isArray(entry)) return entry.join(', ').trim();
        if (typeof entry === 'string') return normalizeAnswer(entry);
        if (entry && typeof entry === 'object') {
          const obj = entry as Record<string, unknown>;
          const val = obj.label || obj.answer || obj.value || obj.text;
          if (typeof val === 'string') return normalizeAnswer(val);
          return JSON.stringify(entry);
        }
        return '';
      })
      .filter(Boolean);
    return answers.length > 0 ? answers : null;
  }
  if (typeof parsed === 'string' && parsed.trim()) {
    const prose = extractProseAnswers(parsed);
    if (prose) return prose;
    return [normalizeAnswer(parsed)];
  }

  // Fallback for non-JSON result
  if (typeof result === 'string') {
    const prose = extractProseAnswers(result);
    if (prose) return prose;
    const raw = normalizeAnswer(result);
    if (raw && raw !== '""' && raw !== '[]') return [raw];
  }
  return null;
}

/**
 * Parse the JSON body of a `question` tool argument blob. The first
 * line is a status marker; the rest is JSON that may either be a
 * bare array of question objects or `{ questions: [...] }`.
 */
export function parseQuestions(argsText: string): QuestionData[] | null {
  const lines = argsText.split('\n');
  const jsonStr = lines.slice(1).join('\n').trim();
  if (!jsonStr) return null;
  try {
    const parsed = JSON.parse(jsonStr);
    const questions = parsed?.questions || parsed;
    if (Array.isArray(questions) && questions.length > 0 && questions[0]?.question) {
      return questions as QuestionData[];
    }
  } catch {
    /* not JSON */
  }
  return null;
}

/** One permission approval attached to the tool call it unblocked. */
export interface ToolApproval {
  /** OpenCode permission type, e.g. `bash` / `edit` / `external_directory`. */
  permission: string;
  /** Resources the approval covered (command, paths, ...). */
  patterns: string[];
  /** Judge's one-line conclusion. Empty for legacy approvals. */
  reasoning: string;
  approvedBy: 'user' | 'ai';
  reply?: 'once' | 'always';
  metadata?: Record<string, unknown>;
  askedAt?: number;
  approvedAt?: number;
}

/**
 * Marker line prefix used to smuggle approvals through the stringly-typed
 * `argsText` channel, same trick as `@time:` / `@user-executed-tool`.
 */
export const APPROVAL_META = '@approved:';

/** Encode one approval as an `argsText` marker line (leading newline). */
export function encodeToolApproval(approval: ToolApproval): string {
  return `\n${APPROVAL_META}${JSON.stringify(approval)}`;
}

/**
 * Pull every `@approved:` marker line out of argsText. Returns the
 * decoded approvals plus argsText with those lines removed so the
 * downstream renderers never see them.
 */
export function parseToolApprovals(argsText: string): {
  approvals: ToolApproval[];
  strippedArgs: string;
} {
  if (!argsText.includes(APPROVAL_META)) return { approvals: [], strippedArgs: argsText };
  const approvals: ToolApproval[] = [];
  const kept = argsText.split('\n').filter((line) => {
    if (!line.startsWith(APPROVAL_META)) return true;
    try {
      const parsed = JSON.parse(line.slice(APPROVAL_META.length)) as Partial<ToolApproval>;
      approvals.push({
        permission: parsed.permission || '',
        patterns: parsed.patterns || [],
        reasoning: parsed.reasoning || '',
        approvedBy: parsed.approvedBy === 'user' ? 'user' : 'ai',
        reply: parsed.reply === 'always' ? 'always' : parsed.reply === 'once' ? 'once' : undefined,
        metadata: parsed.metadata && typeof parsed.metadata === 'object' && !Array.isArray(parsed.metadata)
          ? parsed.metadata
          : undefined,
        askedAt: typeof parsed.askedAt === 'number' ? parsed.askedAt : undefined,
        approvedAt: typeof parsed.approvedAt === 'number' ? parsed.approvedAt : undefined,
      });
    } catch { /* keep the tool renderable on a malformed marker */ }
    return false;
  });
  return { approvals, strippedArgs: kept.join('\n') };
}

/**
 * Marker line prefix carrying a shell tool's human description, so the
 * command itself stays byte-for-byte intact in argsText. Without it a
 * multi-line command (heredoc, quoted block) has its first line
 * mistaken for the description.
 */
export const SHELL_DESC_META = '@desc:';

/**
 * Pull the `@desc:` marker out of argsText. Only the contiguous meta
 * block right after the status line is scanned, so a command whose own
 * body contains an `@desc:` line is left alone.
 */
export function parseShellDescription(argsText: string): { description: string; strippedArgs: string } {
  if (!argsText.includes(SHELL_DESC_META)) return { description: '', strippedArgs: argsText };
  const lines = argsText.split('\n');
  for (let i = 1; i < lines.length && lines[i].startsWith('@'); i++) {
    if (!lines[i].startsWith(SHELL_DESC_META)) continue;
    return {
      description: lines[i].slice(SHELL_DESC_META.length),
      strippedArgs: [...lines.slice(0, i), ...lines.slice(i + 1)].join('\n'),
    };
  }
  return { description: '', strippedArgs: argsText };
}

/**
 * Extract tool timing from the `@time:` line encoded in argsText.
 * Returns `{ startedAt, completedAt }` in unix ms, or null when no
 * timing data is present. Also strips the `@time:` line from the
 * remaining args so downstream parsers don't see it.
 */
export function parseToolTime(argsText: string): {
  startedAt: number;
  completedAt: number;
  strippedArgs: string;
} | null {
  const lines = argsText.split('\n');
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].startsWith('@time:')) {
      const payload = lines[i].slice(6);
      const [startStr, endStr] = payload.split(',');
      const startedAt = parseInt(startStr, 10) || 0;
      const completedAt = parseInt(endStr, 10) || 0;
      if (!startedAt) return null;
      const stripped = [...lines.slice(0, i), ...lines.slice(i + 1)].join('\n');
      return { startedAt, completedAt, strippedArgs: stripped };
    }
  }
  return null;
}

/**
 * Format a duration in milliseconds into a compact human-readable
 * string for tool cards. Uses decimal seconds for sub-minute
 * durations so the reader can see sub-second precision.
 */
export function formatToolDuration(ms: number): string {
  if (ms < 0) return '';
  if (ms < 1000) return '< 1s';
  const s = ms / 1000;
  if (s < 60) return s.toFixed(1) + 's';
  const m = Math.floor(s / 60);
  const rem = Math.round(s % 60);
  if (m < 60) return m + 'm ' + rem + 's';
  const h = Math.floor(m / 60);
  return h + 'h ' + (m % 60) + 'm';
}
