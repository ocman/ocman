import { useCallback, useEffect, useRef, useState } from 'react';
import '../PermissionPrompt.css';

export interface PendingPermission {
  permissionId: string;
  permission: string;
  patterns: string[];
}

type Reply = 'once' | 'always' | 'reject';

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
}: {
  permission: PendingPermission;
  onReply: (reply: Reply) => void;
  disabled?: boolean;
  error?: string | null;
}) {
  const [step, setStep] = useState<'choose' | 'confirm-always'>('choose');
  const [focusedIdx, setFocusedIdx] = useState(0);
  const [confirmIdx, setConfirmIdx] = useState(CONFIRM_DEFAULT_IDX);
  const wrapRef = useRef<HTMLDivElement>(null);

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
  useEffect(() => {
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
      setConfirmIdx(CONFIRM_DEFAULT_IDX);
      setStep('confirm-always');
      return;
    }
    submit(reply);
  }, [disabled, submit]);

  const handleChooseKeyDown = (e: React.KeyboardEvent) => {
    if (disabled) return;

    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      submit('reject');
      return;
    }

    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      pick(CHOICES[focusedIdx].reply);
      return;
    }

    if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
      e.preventDefault();
      setFocusedIdx((i) => (i + 1) % CHOICES.length);
      return;
    }
    if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
      e.preventDefault();
      setFocusedIdx((i) => (i - 1 + CHOICES.length) % CHOICES.length);
      return;
    }

    // Letter hotkeys & 1/2/3. Case-sensitive so 'a' (allow once) and
    // 'A' (allow always) are distinct.
    const hotkeyMatch = CHOICES.findIndex((c) => c.hotkey === e.key);
    if (hotkeyMatch >= 0 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      setFocusedIdx(hotkeyMatch);
      pick(CHOICES[hotkeyMatch].reply);
      return;
    }
    if (e.key >= '1' && e.key <= String(CHOICES.length) && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      const idx = Number(e.key) - 1;
      setFocusedIdx(idx);
      pick(CHOICES[idx].reply);
    }
  };

  const handleConfirmKeyDown = (e: React.KeyboardEvent) => {
    if (disabled) return;

    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      setStep('choose');
      return;
    }

    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      if (CONFIRM_CHOICES[confirmIdx].action === 'confirm') {
        submit('always');
      } else {
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
      setConfirmIdx((i) => (i + 1) % CONFIRM_CHOICES.length);
    }
  };

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
                type="button"
                className={`oc-permission-btn${i === confirmIdx ? ' oc-permission-btn-active' : ''}`}
                onClick={() => {
                  setConfirmIdx(i);
                  if (c.action === 'confirm') submit('always');
                  else setStep('choose');
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
      className="oc-permission-wrap"
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
              disabled={disabled}
              tabIndex={-1}
            >{c.label}</button>
          ))}
          <span className="oc-permission-keys">
            <kbd>↑↓</kbd> move &middot; <kbd>a</kbd>/<kbd>A</kbd>/<kbd>r</kbd> pick &middot; <kbd>enter</kbd> submit &middot; <kbd>esc</kbd> reject
          </span>
        </div>
      </div>
    </div>
  );
}
