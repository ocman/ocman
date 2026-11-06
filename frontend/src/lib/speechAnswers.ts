import type { Message, Part } from './api';
import { parsePart } from './parsePart';
import { speechText } from './speechText';
import type { TurnAggregate, TurnStatsMap } from './turnStats';

const noParts: Part[] = [];
const sameParts = (a: Part[], b: Part[]) => a.length === b.length && a.every((part, i) => part === b[i]);

function isFinalAnswer(message: Message, turn: TurnAggregate | undefined) {
  return turn?.isSummaryAnchor && !turn.isLive && message.data.role === 'assistant'
    && !message.data.error && !message.data.summary && message.data.agent !== 'compaction'
    && ['stop', 'end_turn'].includes(message.data.finish ?? '');
}

/** Per-view index: one parts scan, retaining unchanged completed-answer buckets. */
export function createSpeechAnswers() {
  let indexedParts: Part[] | undefined;
  let index = new Map<string, Part[]>();
  let completed = new Map<string, { parts: Part[]; prose: string }>();
  let answers = new Map<string, string>();
  return (messages: Message[], parts: Part[], turns: TurnStatsMap, excludedId?: string) => {
    if (parts !== indexedParts) {
      const next = new Map<string, Part[]>();
      for (const part of parts) {
        const bucket = next.get(part.messageId);
        if (bucket) bucket.push(part);
        else next.set(part.messageId, [part]);
      }
      for (const [id, bucket] of next) {
        const previous = index.get(id);
        if (previous && sameParts(previous, bucket)) next.set(id, previous);
      }
      index = next;
      indexedParts = parts;
    }
    const nextCompleted = new Map<string, { parts: Part[]; prose: string }>();
    const nextAnswers = new Map<string, string>();
    for (const message of messages) {
      if (!isFinalAnswer(message, turns.get(message.id))) continue;
      const bucket = index.get(message.id) ?? noParts;
      let entry = completed.get(message.id);
      if (!entry || entry.parts !== bucket) {
        const text = bucket.map(parsePart)
          .filter((part) => part.type === 'text' && !part.synthetic && !part.ignored)
          .map((part) => part.text ?? '').join('\n\n').trim();
        entry = { parts: bucket, prose: speechText(text) };
      }
      nextCompleted.set(message.id, entry);
      if (entry.prose && message.id !== excludedId) nextAnswers.set(message.id, entry.prose);
    }
    completed = nextCompleted;
    const previous = [...answers];
    if (previous.length !== nextAnswers.size || [...nextAnswers].some(([id, text], i) =>
      previous[i]?.[0] !== id || previous[i]?.[1] !== text)) answers = nextAnswers;
    return answers;
  };
}

/** Read original text only. Rendered thread text also contains reasoning and errors. */
export function finalAnswers(messages: Message[], parts: Part[], turns: TurnStatsMap): Map<string, string> {
  return createSpeechAnswers()(messages, parts, turns);
}
