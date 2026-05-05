import { describe, it, expect } from 'vitest';
import {
  EXTENSION_LANGUAGE_MAP,
  escapeHtml,
  inferLanguageFromPath,
  inferDiffLanguage,
  highlightDiffCode,
  parseJsonObject,
  parseJsonObjectFromMixedText,
  extractPatchPayload,
  splitToolArgs,
  parsePatchSections,
  shortenPatchPath,
  summarizePatch,
  parseQuestionAnswers,
  parseQuestions,
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

  it('produces some highlighted output for a known language', () => {
    const result = highlightDiffCode('const x = 1;', 'javascript');
    expect(typeof result).toBe('string');
    expect(result.length).toBeGreaterThan(0);
  });

  it('falls back to autodetect when the hint is unknown', () => {
    const result = highlightDiffCode('hello world', 'no-such-language');
    expect(typeof result).toBe('string');
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
});

describe('parsePatchSections', () => {
  it('parses Add / Update / Delete sections', () => {
    const patch = [
      '*** Begin Patch',
      '*** Add File: a.txt',
      '*** Update File: /repo/b.go',
      '*** Delete File: c.md',
      '*** End Patch',
    ].join('\n');
    expect(parsePatchSections(patch)).toEqual([
      { action: 'add', path: 'a.txt' },
      { action: 'update', path: '/repo/b.go' },
      { action: 'delete', path: 'c.md' },
    ]);
  });

  it('returns an empty list when no headers are present', () => {
    expect(parsePatchSections('no patch here')).toEqual([]);
  });
});

describe('shortenPatchPath', () => {
  it('returns relative paths unchanged', () => {
    expect(shortenPatchPath('relative/path.ts')).toBe('relative/path.ts');
    expect(shortenPatchPath('a.txt')).toBe('a.txt');
  });

  it('keeps the marker prefix when the path matches a known root', () => {
    expect(shortenPatchPath('/Users/me/project/frontend/src/x.ts'))
      .toBe('frontend/src/x.ts');
    expect(shortenPatchPath('/abs/repo/internal/server/foo.go'))
      .toBe('internal/server/foo.go');
    expect(shortenPatchPath('/etc/.github/workflows/ci.yml'))
      .toBe('.github/workflows/ci.yml');
  });

  it('falls back to the trailing two segments for unknown roots', () => {
    expect(shortenPatchPath('/var/log/app/api.log')).toBe('app/api.log');
  });
});

describe('summarizePatch', () => {
  it('returns "Patch" when no sections parse', () => {
    expect(summarizePatch('no sections')).toBe('Patch');
  });

  it('returns the action verb + path for a single section', () => {
    expect(summarizePatch('*** Update File: /repo/internal/foo.go'))
      .toBe('Update internal/foo.go');
    expect(summarizePatch('*** Add File: a.txt')).toBe('Add a.txt');
    expect(summarizePatch('*** Delete File: x')).toBe('Delete x');
  });

  it('counts files for multi-section patches', () => {
    const patch = [
      '*** Add File: a',
      '*** Add File: b',
      '*** Update File: c',
      '*** Delete File: d',
    ].join('\n');
    expect(summarizePatch(patch))
      .toBe('Patch 4 files (2 adds, 1 update, 1 delete)');
  });

  it('uses singular form for count of one in multi-section patches', () => {
    const patch = [
      '*** Add File: a',
      '*** Update File: b',
    ].join('\n');
    // 2 sections -> multi-section path
    expect(summarizePatch(patch)).toBe('Patch 2 files (1 add, 1 update)');
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

describe('parseQuestions', () => {
  it('parses a `{ questions: [...] }` envelope', () => {
    const argsText = `pending\n${JSON.stringify({
      questions: [
        { header: 'h', question: 'q1', options: [] },
      ],
    })}`;
    expect(parseQuestions(argsText)).toEqual([
      { header: 'h', question: 'q1', options: [] },
    ]);
  });

  it('parses a bare array', () => {
    const argsText = `running\n${JSON.stringify([
      { header: 'h', question: 'q', options: [] },
    ])}`;
    expect(parseQuestions(argsText)).toEqual([
      { header: 'h', question: 'q', options: [] },
    ]);
  });

  it('returns null when there is no payload after the status line', () => {
    expect(parseQuestions('only one line')).toBeNull();
  });

  it('returns null for malformed JSON', () => {
    expect(parseQuestions('status\n{not json')).toBeNull();
  });

  it('returns null when the payload does not look like questions', () => {
    expect(parseQuestions('status\n[]')).toBeNull();
    expect(parseQuestions('status\n[{}]')).toBeNull();
  });
});
