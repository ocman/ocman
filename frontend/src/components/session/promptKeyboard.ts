// Shared keyboard coordination protocol for the in-session prompt
// components (PermissionPrompt, QuestionPrompt). Both register a
// capture-phase window keydown listener and mark events they consume so
// the *other* prompt's listener — and the global app shortcuts — skip an
// already-handled key. Keeping this marker logic in one place avoids the
// two prompts drifting into incompatible copies of the protocol.

export type PromptKeyEvent = KeyboardEvent | React.KeyboardEvent<HTMLDivElement>;

const PROMPT_EVENT_HANDLED = '__ocmanPromptHandled';

/**
 * Reports whether a prompt has already consumed this key event. Checks
 * both the event object itself (native keydown) and its nativeEvent —
 * React synthetic events wrap the native event but don't copy custom
 * properties set on it, so a mark on one isn't visible on the other
 * without this bridge.
 */
export function wasHandledByPrompt(e: PromptKeyEvent): boolean {
  if ((e as PromptKeyEvent & Record<string, unknown>)[PROMPT_EVENT_HANDLED]) return true;
  const native = (e as React.KeyboardEvent<HTMLDivElement>).nativeEvent;
  if (native && (native as KeyboardEvent & Record<string, unknown>)[PROMPT_EVENT_HANDLED]) return true;
  return false;
}

/** Marks a native keydown event as consumed by a prompt. */
export function markHandledByPrompt(e: KeyboardEvent): void {
  (e as KeyboardEvent & Record<string, unknown>)[PROMPT_EVENT_HANDLED] = true;
}
