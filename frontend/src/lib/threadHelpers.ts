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

  if (toolName === 'apply_patch' && (firstLine.trim().startsWith('{') || !rest)) {
    return { title: '', detail: rawArgs.trim() };
  }

  return { title: firstLine, detail: rest };
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
