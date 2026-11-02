/** One option of an MCP-style question. */
export interface QuestionOption {
  label: string;
  description: string;
}

/** A single MCP-style question with its prompt header and option list. */
export interface QuestionItem {
  question: string;
  header: string;
  options: QuestionOption[];
  multiple?: boolean;
  custom?: boolean;
}

/**
 * Coerce a question payload (string / object / array) into a flat
 * `QuestionItem[]`. Strings are JSON-parsed first; wrapper objects
 * with a `questions` field are unwrapped; entries lacking a string
 * `question` or array `options` are dropped.
 */
export function normalizeQuestionItems(raw: unknown): QuestionItem[] {
  let value = raw;

  if (typeof value === 'string') {
    try {
      value = JSON.parse(value) as unknown;
    } catch {
      return [];
    }
  }

  if (value && typeof value === 'object' && !Array.isArray(value)) {
    const obj = value as Record<string, unknown>;
    if (Array.isArray(obj.questions)) value = obj.questions;
  }

  if (!Array.isArray(value) || value.length === 0) return [];

  return value.filter(
    (q): q is QuestionItem =>
      !!q &&
      typeof q === 'object' &&
      typeof (q as QuestionItem).question === 'string' &&
      Array.isArray((q as QuestionItem).options),
  );
}

/**
 * Parse the JSON body of a `question` tool argument blob. The first
 * line is a status marker; the rest is JSON that may either be a
 * bare array of question objects or `{ questions: [...] }`.
 */
export function parseQuestions(argsText: string): QuestionItem[] | null {
  const questions = normalizeQuestionItems(argsText.split('\n').slice(1).join('\n').trim());
  return questions.length > 0 ? questions : null;
}
