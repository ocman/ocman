import { useCallback, useEffect, useRef, useState } from 'react';
import '../PermissionPrompt.css';

export interface PendingPermission {
  permissionId: string;
  permission: string;
  patterns: string[];
}

type Reply = 'once' | 'always' | 'reject';

const CHOICES: { reply: Reply; label: string; hotkey: string }[] = [
  { reply: 'once', label: 'Allow once', hotkey: 'o' },
  { reply: 'always', label: 'Allow always', hotkey: 'a' },
  { reply: 'reject', label: 'Reject', hotkey: 'r' },
];

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
  const [focusedIdx, setFocusedIdx] = useState(0);
  const wrapRef = useRef<HTMLDivElement>(null);

  // Auto-focus on mount so keys work without a click.
  useEffect(() => {
    wrapRef.current?.focus();
  }, []);

  const submit = useCallback((reply: Reply) => {
    if (disabled) return;
    onReply(reply);
  }, [disabled, onReply]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (disabled) return;

    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      submit('reject');
      return;
    }

    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      submit(CHOICES[focusedIdx].reply);
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

    // Letter hotkeys & 1/2/3
    const lower = e.key.toLowerCase();
    const hotkeyMatch = CHOICES.findIndex((c) => c.hotkey === lower);
    if (hotkeyMatch >= 0 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      setFocusedIdx(hotkeyMatch);
      submit(CHOICES[hotkeyMatch].reply);
      return;
    }
    if (e.key >= '1' && e.key <= String(CHOICES.length) && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      const idx = Number(e.key) - 1;
      setFocusedIdx(idx);
      submit(CHOICES[idx].reply);
    }
  };

  return (
    <div
      ref={wrapRef}
      className="oc-permission-wrap"
      tabIndex={-1}
      role="dialog"
      aria-label="Permission required"
      onKeyDown={handleKeyDown}
    >
      <div className="oc-permission-box">
        <div className="oc-permission-header">
          <span className="oc-permission-icon">&#9651;</span>
          <span>Permission required</span>
        </div>
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
        <div className="oc-permission-actions">
          {CHOICES.map((c, i) => (
            <button
              key={c.reply}
              type="button"
              className={`oc-permission-btn${i === focusedIdx ? ' oc-permission-btn-active' : ''}`}
              onClick={() => { setFocusedIdx(i); submit(c.reply); }}
              onMouseEnter={() => setFocusedIdx(i)}
              disabled={disabled}
              tabIndex={-1}
            >{c.label}</button>
          ))}
          <span className="oc-permission-keys">
            <kbd>↑↓</kbd> move &middot; <kbd>o</kbd>/<kbd>a</kbd>/<kbd>r</kbd> pick &middot; <kbd>enter</kbd> submit &middot; <kbd>esc</kbd> reject
          </span>
        </div>
      </div>
    </div>
  );
}
