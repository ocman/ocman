import { parseShellEnvelope } from './shellEnvelope';
import { parseSubagentEnvelope } from './subagentEnvelope';

const HAND_BACK_TAG = '[handback-send-enforce] ';
const HAND_BACK_REMINDER = 'Your report has not been delivered. Call SubagentHandback({message: <your full report>}) now; the call ends your run.';

export function parseV2Notification(text: string) {
  const shell = parseShellEnvelope(text);
  if (shell) return { kind: 'shell' as const, shell };
  const subagent = parseSubagentEnvelope(text);
  if (subagent) return { kind: 'subagent' as const, subagent };
  // Match the full harness reminder, not ordinary prompts mentioning its tag.
  if (text.trim() === HAND_BACK_TAG + HAND_BACK_REMINDER) {
    return { kind: 'handback-reminder' as const, body: HAND_BACK_REMINDER };
  }
  return null;
}
