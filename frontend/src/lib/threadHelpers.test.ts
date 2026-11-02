import { describe, it, expect, vi } from 'vitest';
import hljs from 'highlight.js/lib/common';
import { highlighter } from './highlighter';
import {
  EXTENSION_LANGUAGE_MAP,
  escapeHtml,
  inferLanguageFromPath,
  inferDiffLanguage,
  highlightDiffCode,
  toolOutputLanguage,
  parseJsonObject,
  parseJsonObjectFromMixedText,
  extractPatchPayload,
  splitToolArgs,
  summarizeToolArgs,
  parseQuestionAnswers,
} from './threadHelpers';

describe('escapeHtml', () => {
  it('escapes the five HTML-significant characters', () => {
    expect(escapeHtml(`<a href="x">&'</a>`))
      .toBe('&lt;a href=&quot;x&quot;&gt;&amp;&#39;&lt;/a&gt;');
  });

  it('returns plain text unchanged', () => {
    expect(escapeHtml('hello world')).toBe('hello world');
  });

  it('handles the empty string', () => {
    expect(escapeHtml('')).toBe('');
  });

  it('escapes ampersands before they could create entities', () => {
    // & must be escaped first so &lt; doesn't become &amp;lt;
    expect(escapeHtml('a & <b>')).toBe('a &amp; &lt;b&gt;');
  });
});

describe('inferLanguageFromPath', () => {
  it('returns dockerfile for an exact Dockerfile basename', () => {
    expect(inferLanguageFromPath('/repo/Dockerfile')).toBe('dockerfile');
    expect(inferLanguageFromPath('Dockerfile')).toBe('dockerfile');
  });

  it('looks up known extensions in the map', () => {
    expect(inferLanguageFromPath('main.go')).toBe('go');
    expect(inferLanguageFromPath('src/foo.tsx')).toBe('typescript');
    expect(inferLanguageFromPath('script.PY')).toBe('python'); // case-insensitive ext
  });

  it('returns undefined for unknown extensions and missing extensions', () => {
    expect(inferLanguageFromPath('README')).toBeUndefined();
    expect(inferLanguageFromPath('notes.unknown')).toBeUndefined();
    expect(inferLanguageFromPath('')).toBeUndefined();
  });

  it('handles backslash-separated Windows paths', () => {
    expect(inferLanguageFromPath('C:\\src\\app.ts')).toBe('typescript');
  });

  it('exposes a populated extension map (sanity)', () => {
    expect(EXTENSION_LANGUAGE_MAP.json).toBe('json');
    expect(EXTENSION_LANGUAGE_MAP.zsh).toBe('bash');
  });
});

describe('inferDiffLanguage', () => {
  it('strips the Edit/Write prefix and resolves the path', () => {
    expect(inferDiffLanguage('Edit src/foo.go', '')).toBe('go');
    expect(inferDiffLanguage('Write app/main.py', '')).toBe('python');
  });

  it('falls back to the first detail line when the title has no extension', () => {
    expect(inferDiffLanguage('apply_patch', 'app/main.rs')).toBe('rust');
  });

  it('returns undefined when nothing yields a language', () => {
    expect(inferDiffLanguage('Random title', 'no path here')).toBeUndefined();
  });
});

describe('highlightDiffCode', () => {
  it('returns empty string for empty input', () => {
    expect(highlightDiffCode('')).toBe('');
  });

  it('escapes the code until highlight.js has loaded, then highlights it', async () => {
    expect(highlighter.get()).toBeUndefined();
    expect(highlightDiffCode('a < b', 'javascript')).toBe('a &lt; b');
    await highlighter.load();
    expect(highlightDiffCode('const x = 1;', 'javascript')).toContain('<span class="hljs-keyword">const</span>');
  });

  it('escapes instead of autodetecting when the hint is missing or unknown', async () => {
    // highlightAuto runs every grammar; per diff line that dominated
    // tool-output rendering.
    await highlighter.load();
    const auto = vi.spyOn(hljs, 'highlightAuto');
    expect(highlightDiffCode('<b>x</b>', 'no-such-language')).toBe('&lt;b&gt;x&lt;/b&gt;');
    expect(highlightDiffCode('<b>x</b>')).toBe('&lt;b&gt;x&lt;/b&gt;');
    expect(auto).not.toHaveBeenCalled();
    auto.mockRestore();
  });
});

describe('toolOutputLanguage', () => {
  it('derives the language from the title or the file-path argument', () => {
    expect(toolOutputLanguage('Edit src/foo.go', '')).toBe('go');
    expect(toolOutputLanguage('', '{"filePath": "app/main.py", "oldString": "x"}')).toBe('python');
    expect(toolOutputLanguage('', '{"file_path": "lib/a.rs"}')).toBe('rust');
    expect(toolOutputLanguage('', '{"path": "Dockerfile"}')).toBe('dockerfile');
  });

  it('returns undefined when no path is known', () => {
    expect(toolOutputLanguage('', '{"query": "x"}')).toBeUndefined();
    expect(toolOutputLanguage('', 'not json')).toBeUndefined();
  });
});

describe('parseJsonObject', () => {
  it('parses a simple object', () => {
    expect(parseJsonObject('{"a": 1}')).toEqual({ a: 1 });
  });

  it('rejects arrays', () => {
    expect(parseJsonObject('[1,2,3]')).toBeNull();
  });

  it('rejects primitives', () => {
    expect(parseJsonObject('"hello"')).toBeNull();
    expect(parseJsonObject('42')).toBeNull();
    expect(parseJsonObject('null')).toBeNull();
  });

  it('rejects malformed JSON', () => {
    expect(parseJsonObject('{a: 1}')).toBeNull();
    expect(parseJsonObject('{"a":')).toBeNull();
  });

  it('rejects strings with content outside the braces', () => {
    expect(parseJsonObject('garbage {"a":1}')).toBeNull();
  });

  it('tolerates leading/trailing whitespace', () => {
    expect(parseJsonObject('  {"a":1}  ')).toEqual({ a: 1 });
  });
});

describe('parseJsonObjectFromMixedText', () => {
  it('extracts an embedded object from prose', () => {
    expect(parseJsonObjectFromMixedText('Result: {"a":1}.')).toEqual({ a: 1 });
  });

  it('returns null when no object is present', () => {
    expect(parseJsonObjectFromMixedText('no braces here')).toBeNull();
  });

  it('returns null when the slice does not parse', () => {
    expect(parseJsonObjectFromMixedText('a { not json } b')).toBeNull();
  });
});

describe('extractPatchPayload', () => {
  it('returns the patchText and a preamble for prose-wrapped JSON', () => {
    const text = 'Applying patch:\n{"patchText":"*** Begin Patch\\n*** End Patch"}';
    const out = extractPatchPayload(text);
    expect(out.patchText).toBe('*** Begin Patch\n*** End Patch');
    expect(out.preamble).toBe('Applying patch:');
  });

  it('returns the full text as preamble when no patch is detected', () => {
    const out = extractPatchPayload('plain text');
    expect(out.patchText).toBeNull();
    expect(out.preamble).toBe('plain text');
  });

  it('handles JSON without a leading preamble', () => {
    const out = extractPatchPayload('{"patchText":"x"}');
    expect(out.patchText).toBe('x');
    expect(out.preamble).toBe('');
  });

  it('detects raw apply_patch text without a JSON wrapper', () => {
    const out = extractPatchPayload('Applying patch\n*** Begin Patch\n*** Add File: a.txt\n+x\n*** End Patch');
    expect(out.patchText).toBe('*** Begin Patch\n*** Add File: a.txt\n+x\n*** End Patch');
    expect(out.preamble).toBe('Applying patch');
  });
});

describe('splitToolArgs', () => {
  it('treats the first line as the title', () => {
    const out = splitToolArgs('read', 'file.go\nbody line 1\nbody line 2');
    expect(out.title).toBe('file.go');
    expect(out.detail).toBe('body line 1\nbody line 2');
  });

  it('returns an empty title for apply_patch JSON payloads', () => {
    const out = splitToolArgs('apply_patch', '{"patchText":"x"}');
    expect(out.title).toBe('');
    expect(out.detail).toBe('{"patchText":"x"}');
  });

  it('returns an empty title for apply_patch single-line input', () => {
    const out = splitToolArgs('apply_patch', 'plain title');
    expect(out.title).toBe('');
    expect(out.detail).toBe('plain title');
  });

  it('handles single-line input for non-patch tools', () => {
    const out = splitToolArgs('read', 'only line');
    expect(out.title).toBe('only line');
    expect(out.detail).toBe('');
  });

  it('keeps multi-line JSON payloads together instead of using the opening brace as a title', () => {
    const payload = [
      '{',
      '  "target": "convertMessages",',
      '  "direction": "upstream"',
      '}',
    ].join('\n');
    const out = splitToolArgs('codegraph_impact', payload);
    expect(out.title).toBe('');
    expect(out.detail).toBe(payload);
  });
});

describe('summarizeToolArgs', () => {
  it('picks the highest-priority key from a JSON payload', () => {
    const payload = JSON.stringify({
      direction: 'upstream',
      target: 'convertMessages',
      maxDepth: 3,
    });
    expect(summarizeToolArgs(payload)).toBe('target="convertMessages"');
  });

  it('uses intent when no higher-priority key is present', () => {
    const payload = JSON.stringify({ intent: 'refactor the thread component', branch: 'x' });
    expect(summarizeToolArgs(payload)).toBe('intent="refactor the thread component"');
  });

  it('falls back to the first scalar entry when no priority keys match', () => {
    const payload = JSON.stringify({ depth: 4, weird: 'value' });
    expect(summarizeToolArgs(payload)).toBe('depth=4');
  });

  it('truncates long values with an ellipsis', () => {
    const long = 'x'.repeat(120);
    const payload = JSON.stringify({ target: long });
    const out = summarizeToolArgs(payload);
    expect(out.startsWith('target="')).toBe(true);
    expect(out.endsWith('\u2026"')).toBe(true);
    expect(out.length).toBeLessThanOrEqual('target="'.length + 60 + 1);
  });

  it('handles array payloads by reporting the count', () => {
    expect(summarizeToolArgs('[]')).toBe('[0 items]');
    expect(summarizeToolArgs('[1]')).toBe('[1 item]');
    expect(summarizeToolArgs('[1, 2, 3]')).toBe('[3 items]');
  });

  it('returns the first non-empty line for plain-text args', () => {
    expect(summarizeToolArgs('hello world\nsecond line')).toBe('hello world');
  });

  it('returns an empty string for blank input', () => {
    expect(summarizeToolArgs('')).toBe('');
    expect(summarizeToolArgs('   ')).toBe('');
  });

  it('returns an empty string for balanced but malformed JSON', () => {
    expect(summarizeToolArgs('{not valid json}')).toBe('');
  });

  it('treats unbalanced input as plain text', () => {
    expect(summarizeToolArgs('{not valid json')).toBe('{not valid json');
  });

  it('returns an empty string when JSON object has no usable scalar values', () => {
    expect(summarizeToolArgs(JSON.stringify({ nested: { a: 1 } }))).toBe('');
  });

  it('skips priority keys whose values are non-scalar', () => {
    const payload = JSON.stringify({ target: { complex: true }, name: 'fallback' });
    expect(summarizeToolArgs(payload)).toBe('name="fallback"');
  });
});

describe('parseQuestionAnswers', () => {
  it('returns null for non-string input', () => {
    expect(parseQuestionAnswers(42)).toBeNull();
    expect(parseQuestionAnswers(null)).toBeNull();
    expect(parseQuestionAnswers([])).toBeNull();
  });

  it('returns null for empty / whitespace strings', () => {
    expect(parseQuestionAnswers('')).toBeNull();
    expect(parseQuestionAnswers('   ')).toBeNull();
  });

  it('parses a JSON array of strings', () => {
    expect(parseQuestionAnswers('["a","b"]')).toEqual(['a', 'b']);
  });

  it('joins sub-arrays into a single answer', () => {
    expect(parseQuestionAnswers('[["x","y"]]')).toEqual(['x, y']);
  });

  it('extracts label/answer/value/text from object answers', () => {
    expect(parseQuestionAnswers('[{"label":"L1"}]')).toEqual(['L1']);
    expect(parseQuestionAnswers('[{"answer":"A1"}]')).toEqual(['A1']);
    expect(parseQuestionAnswers('[{"value":"V1"}]')).toEqual(['V1']);
    expect(parseQuestionAnswers('[{"text":"T1"}]')).toEqual(['T1']);
  });

  it('decodes double-encoded JSON', () => {
    // outer JSON is a string that itself contains JSON
    const doubleEncoded = JSON.stringify(JSON.stringify(['only']));
    expect(parseQuestionAnswers(doubleEncoded)).toEqual(['only']);
  });

  it('parses the prose "Q"="A" format', () => {
    const prose =
      'User has answered your questions: "What now?"="Continue", "Next?"="Stop". You can now …';
    expect(parseQuestionAnswers(prose)).toEqual(['Continue', 'Stop']);
  });

  it('handles escaped quotes inside prose answers', () => {
    const prose = '"Q"="line one\\"with quote"';
    expect(parseQuestionAnswers(prose)).toEqual(['line one"with quote']);
  });

  it('strips wrapping quotes from a single-string answer', () => {
    expect(parseQuestionAnswers('"\\"single\\""')).toEqual(['single']);
  });

  it('ignores empty / placeholder answers in fallback path', () => {
    // The string '""' is JSON-parseable to "", so fallback runs and rejects.
    expect(parseQuestionAnswers('""')).toBeNull();
    expect(parseQuestionAnswers('[]')).toBeNull();
  });
});

