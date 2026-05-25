import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import '../PermissionPrompt.css';

export interface PendingPermission {
  permissionId: string;
  permission: string;
  patterns: string[];
}

type Reply = 'once' | 'always' | 'reject';
type KeyEvent = KeyboardEvent | React.KeyboardEvent<HTMLDivElement>;
const PROMPT_EVENT_HANDLED = '__ocmanPromptHandled';

function wasHandledByPrompt(e: KeyEvent): boolean {
  // Check both the event object itself (native keydown events) and the
  // underlying native event (React synthetic events wrap the native event
  // but don't copy custom properties set on it).
  if ((e as KeyEvent & Record<string, unknown>)[PROMPT_EVENT_HANDLED]) return true;
  const native = (e as React.KeyboardEvent<HTMLDivElement>).nativeEvent;
  if (native && (native as KeyboardEvent & Record<string, unknown>)[PROMPT_EVENT_HANDLED]) return true;
  return false;
}

function markHandledByPrompt(e: KeyboardEvent): void {
  (e as KeyboardEvent & Record<string, unknown>)[PROMPT_EVENT_HANDLED] = true;
}

function numberKeyIndex(e: KeyEvent, max: number): number {
  const codeMatch = /^Digit([1-9])$/.exec(e.code) ?? /^Numpad([1-9])$/.exec(e.code);
  if (codeMatch) {
    const idx = Number(codeMatch[1]) - 1;
    return idx < max ? idx : -1;
  }
  if (e.key >= '1' && e.key <= String(max)) {
    return Number(e.key) - 1;
  }
  return -1;
}

function choiceHotkeyIndex(e: KeyEvent): number {
  if (e.code === 'KeyA') return e.shiftKey ? 1 : 0;
  if (e.code === 'KeyR' && !e.shiftKey) return 2;
  return CHOICES.findIndex((c) => c.hotkey === e.key);
}

const CHOICES: { reply: Reply; label: string; hotkey: string }[] = [
  { reply: 'once', label: 'Allow once', hotkey: 'a' },
  { reply: 'always', label: 'Allow always', hotkey: 'A' },
  { reply: 'reject', label: 'Reject', hotkey: 'r' },
];

// Confirmation step (shown after the user picks "Allow always"). Mirrors
// OpenCode's TUI: Cancel is focused by default so an accidental Enter
// doesn't commit a broad allow-rule.
const CONFIRM_CHOICES: { action: 'confirm' | 'cancel'; label: string }[] = [
  { action: 'confirm', label: 'Confirm' },
  { action: 'cancel', label: 'Cancel' },
];
const CONFIRM_DEFAULT_IDX = 1; // Cancel

export function PermissionPrompt({
  permission,
  onReply,
  disabled,
  error,
  autoApproveCapable,
  autoApproveEnabled,
  autoApproveChecking,
  judgeSessionId,
  onEnableAutoApprove,
  onViewJudgeSession,
}: {
  permission: PendingPermission;
  onReply: (reply: Reply) => void;
  disabled?: boolean;
  error?: string | null;
  /** Whether the platform supports auto-approve at all. */
  autoApproveCapable?: boolean;
  /** Whether auto-approve is currently enabled for this session. */
  autoApproveEnabled?: boolean;
  /** True while the LLM judge is evaluating this permission. */
  autoApproveChecking?: boolean;
  /**
   * Session ID of the OpenCode judge session, when available.
   * Shown as a "View reasoning" link so the user can inspect the model's thinking.
   */
  judgeSessionId?: string | null;
  /** Called when the user clicks "Enable auto-approve". */
  onEnableAutoApprove?: () => void;
  /** Called when the user clicks "View reasoning" — navigates to the judge session. */
  onViewJudgeSession?: (sessionId: string) => void;
}) {
  const [step, setStep] = useState<'choose' | 'confirm-always'>('choose');
  const [focusedIdx, setFocusedIdx] = useState(0);
  const [confirmIdx, setConfirmIdx] = useState(CONFIRM_DEFAULT_IDX);
  const wrapRef = useRef<HTMLDivElement>(null);
  const confirmButtonRef = useRef<HTMLButtonElement>(null);
  const cancelButtonRef = useRef<HTMLButtonElement>(null);
  const stepRef = useRef(step);
  const focusedIdxRef = useRef(focusedIdx);
  const confirmIdxRef = useRef(confirmIdx);
  const handleChooseKeyDownRef = useRef<((e: KeyEvent) => void) | null>(null);
  const handleConfirmKeyDownRef = useRef<((e: KeyEvent) => void) | null>(null);
  useLayoutEffect(() => {
    stepRef.current = step;
    focusedIdxRef.current = focusedIdx;
    confirmIdxRef.current = confirmIdx;
  }, [step, focusedIdx, confirmIdx]);

  // If the permission itself changes (new prompt pushed in), reset back to
  // the chooser. Tracking the last-seen id and resetting during render is
  // the idiomatic React pattern for "derive state from props" — avoids the
  // extra effect-driven re-render.
  const [lastPermissionId, setLastPermissionId] = useState(permission.permissionId);
  if (lastPermissionId !== permission.permissionId) {
    setLastPermissionId(permission.permissionId);
    setStep('choose');
    setFocusedIdx(0);
    setConfirmIdx(CONFIRM_DEFAULT_IDX);
  }

  // Auto-focus on mount and when the step changes so keys work without a click.
  useLayoutEffect(() => {
    if (step === 'confirm-always') {
      cancelButtonRef.current?.focus();
      return;
    }
    wrapRef.current?.focus();
  }, [step]);

  const submit = useCallback((reply: Reply) => {
    if (disabled) return;
    onReply(reply);
  }, [disabled, onReply]);

  const pick = useCallback((reply: Reply) => {
    if (disabled) return;
    if (reply === 'always') {
      // Two-step: show a confirmation screen with the patterns that will be
      // allowed. Matches OpenCode's TUI behavior.
      confirmIdxRef.current = CONFIRM_DEFAULT_IDX;
      stepRef.current = 'confirm-always';
      setConfirmIdx(CONFIRM_DEFAULT_IDX);
      setStep('confirm-always');
      return;
    }
    submit(reply);
  }, [disabled, submit]);

  const handleChooseKeyDown = useCallback((e: KeyEvent) => {
    if (disabled || wasHandledByPrompt(e)) return;

    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      submit('reject');
      return;
    }

    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      pick(CHOICES[focusedIdxRef.current].reply);
      return;
    }

    if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
      e.preventDefault();
      setFocusedIdx((i) => {
        const next = (i + 1) % CHOICES.length;
        focusedIdxRef.current = next;
        return next;
      });
      return;
    }
    if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
      e.preventDefault();
      setFocusedIdx((i) => {
        const next = (i - 1 + CHOICES.length) % CHOICES.length;
        focusedIdxRef.current = next;
        return next;
      });
      return;
    }

    // Letter hotkeys & 1/2/3. Case-sensitive so 'a' (allow once) and
    // 'A' (allow always) are distinct.
    const hotkeyMatch = choiceHotkeyIndex(e);
    if (hotkeyMatch >= 0 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      focusedIdxRef.current = hotkeyMatch;
      setFocusedIdx(hotkeyMatch);
      pick(CHOICES[hotkeyMatch].reply);
      return;
    }
    const idx = numberKeyIndex(e, CHOICES.length);
    if (idx >= 0 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      focusedIdxRef.current = idx;
      setFocusedIdx(idx);
      pick(CHOICES[idx].reply);
    }
  }, [disabled, pick, submit]);

  const handleConfirmKeyDown = useCallback((e: KeyEvent) => {
    if (disabled || wasHandledByPrompt(e)) return;

    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      stepRef.current = 'choose';
      setStep('choose');
      return;
    }

    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      if (CONFIRM_CHOICES[confirmIdxRef.current].action === 'confirm') {
        submit('always');
      } else {
        stepRef.current = 'choose';
        setStep('choose');
      }
      return;
    }

    if (
      e.key === 'ArrowRight' || e.key === 'ArrowDown'
      || e.key === 'ArrowLeft' || e.key === 'ArrowUp'
      || e.key === 'Tab'
    ) {
      e.preventDefault();
      const next = (confirmIdxRef.current + 1) % CONFIRM_CHOICES.length;
      confirmIdxRef.current = next;
      setConfirmIdx(next);
      const targetRef = CONFIRM_CHOICES[next].action === 'confirm' ? confirmButtonRef : cancelButtonRef;
      // Defer focus so the browser has finished processing the keydown event
      // before we move focus programmatically (synchronous focus() calls inside
      // keydown handlers are silently ignored in some browsers).
      setTimeout(() => { targetRef.current?.focus(); }, 0);
    }
  }, [disabled, submit]);

  // Keep stable refs so the window listener always calls the latest handlers
  // regardless of when deps (like onReply) change due to SSE updates.
  useLayoutEffect(() => {
    handleChooseKeyDownRef.current = handleChooseKeyDown;
    handleConfirmKeyDownRef.current = handleConfirmKeyDown;
  });

  useLayoutEffect(() => {
    const onWindowKeyDown = (e: KeyboardEvent) => {
      if (wasHandledByPrompt(e)) return;
      if (stepRef.current === 'confirm-always') {
        handleConfirmKeyDownRef.current?.(e);
        markHandledByPrompt(e);
        return;
      }
      handleChooseKeyDownRef.current?.(e);
      markHandledByPrompt(e);
    };
    window.addEventListener('keydown', onWindowKeyDown, true);
    return () => window.removeEventListener('keydown', onWindowKeyDown, true);
  }, []);

  if (step === 'confirm-always') {
    return (
      <div
        ref={wrapRef}
        className="oc-permission-wrap"
        tabIndex={-1}
        role="dialog"
        aria-label="Confirm always allow"
        onKeyDown={handleConfirmKeyDown}
      >
        <div className="oc-permission-box">
          <div className="oc-permission-header">
            <span className="oc-permission-icon">&#9651;</span>
            <span>Always allow</span>
          </div>
          <div className="oc-permission-content">
            <div className="oc-permission-desc">
              This will allow the following patterns for the remainder of this session.
            </div>
            {permission.patterns.length > 0 ? (
              <div className="oc-permission-patterns">
                {permission.patterns.map((p) => (
                  <div key={p} className="oc-permission-pattern">- {p}</div>
                ))}
              </div>
            ) : (
              <div className="oc-permission-patterns">
                <div className="oc-permission-pattern">(no patterns reported)</div>
              </div>
            )}
            {error && (
              <div className="oc-permission-error">{error}</div>
            )}
          </div>
          <div className="oc-permission-actions">
            {CONFIRM_CHOICES.map((c, i) => (
              <button
                key={c.action}
                ref={c.action === 'confirm' ? confirmButtonRef : cancelButtonRef}
                type="button"
                className={`oc-permission-btn${i === confirmIdx ? ' oc-permission-btn-active' : ''}`}
                onClick={() => {
                  setConfirmIdx(i);
                  confirmIdxRef.current = i;
                  if (c.action === 'confirm') submit('always');
                  else {
                    stepRef.current = 'choose';
                    setStep('choose');
                  }
                }}

                onMouseEnter={() => setConfirmIdx(i)}
                disabled={disabled}
                tabIndex={-1}
              >{c.label}</button>
            ))}
            <span className="oc-permission-keys">
              <kbd>↹</kbd> select &middot; <kbd>enter</kbd> confirm &middot; <kbd>esc</kbd> back
            </span>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div
      ref={wrapRef}
      className={`oc-permission-wrap${autoApproveChecking ? ' oc-permission-wrap--checking' : ''}`}
      tabIndex={-1}
      role="dialog"
      aria-label="Permission required"
      onKeyDown={handleChooseKeyDown}
    >
      <div className="oc-permission-box">
        <div className="oc-permission-header">
          <span className="oc-permission-icon">&#9651;</span>
          <span>Permission required</span>
        </div>
        {(autoApproveChecking || judgeSessionId) && (
          <div className="oc-permission-ai-row" aria-live="polite">
            {autoApproveChecking ? (
              <>
                <span className="oc-spinner oc-permission-ai-spinner" />
                <span className="oc-permission-ai-label">AI is reviewing this permission&hellip;</span>
              </>
            ) : (
              <>
                <span className="oc-permission-ai-label oc-permission-ai-label--done">
                  AI flagged for review
                </span>
                {judgeSessionId && onViewJudgeSession && (
                  <button
                    type="button"
                    className="oc-permission-judge-link"
                    onClick={() => onViewJudgeSession(judgeSessionId)}
                    data-testid="view-judge-session"
                  >
                    View reasoning
                  </button>
                )}
              </>
            )}
          </div>
        )}
        <div className="oc-permission-content">
          <div className="oc-permission-desc">
            &larr; {permission.permission}
          </div>
          {permission.patterns.length > 0 && (
            <div className="oc-permission-patterns">
              <div className="oc-permission-patterns-label">Patterns</div>
              {permission.patterns.map((p) => (
                <div key={p} className="oc-permission-pattern">- {p}</div>
              ))}
            </div>
          )}
          {error && (
            <div className="oc-permission-error">{error}</div>
          )}
        </div>
        <div className="oc-permission-actions">
          {CHOICES.map((c, i) => (
            <button
              key={c.reply}
              type="button"
              className={`oc-permission-btn${i === focusedIdx ? ' oc-permission-btn-active' : ''}`}
              onClick={() => { setFocusedIdx(i); pick(c.reply); }}
              onMouseEnter={() => setFocusedIdx(i)}
              disabled={disabled || autoApproveChecking}
              tabIndex={-1}
            >{c.label}</button>
          ))}
          <span className="oc-permission-keys">
            <kbd>↑↓</kbd> move &middot; <kbd>a</kbd>/<kbd>A</kbd>/<kbd>r</kbd> pick &middot; <kbd>enter</kbd> submit &middot; <kbd>esc</kbd> reject
          </span>
        </div>
        {autoApproveCapable && (
          <div className="oc-permission-autoapprove">
            {!autoApproveEnabled ? (
              <button
                type="button"
                className="oc-permission-autoapprove-btn"
                onClick={onEnableAutoApprove}
                data-testid="enable-auto-approve"
              >
                Enable auto-approve for this session
              </button>
            ) : (
              <span className="oc-permission-autoapprove-on">
                Auto-approve on
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
