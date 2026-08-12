import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Modal } from '../Modal';
import '../CommandPalette.css';
import './ReasoningPicker.css';

/** Sentinel value for the "use platform default" option. */
const DEFAULT_VALUE = '';
const DEFAULT_LABEL = 'default';

export interface ReasoningPickerProps {
  open: boolean;
  options: string[];
  current?: string;
  onSelect: (value: string) => void;
  onClose: () => void;
}

/**
 * Command-palette-style modal for picking a reasoning / thinking-budget
 * level. Visually consistent with ModelPicker.
 *
 * The first row is always "default" — selecting it clears the override so
 * the platform's own default applies.
 *
 * Rows are buttons and the arrow keys move DOM focus between them: there
 * is no search input here to host the keyboard model, so focus has to
 * live on the options themselves.
 */
export function ReasoningPicker({
  open,
  options,
  current,
  onSelect,
  onClose,
}: ReasoningPickerProps) {
  // Prepend the "default" sentinel so the user can always opt out of an
  // explicit override.
  const allOptions = useMemo(() => [DEFAULT_VALUE, ...options], [options]);

  // Parent re-mounts this component on each open (conditional render), so
  // the useState initializer picks up the correct index every time.
  const [selectedIndex, setSelectedIndex] = useState(() => {
    const idx = current ? allOptions.indexOf(current) : 0;
    return idx >= 0 ? idx : 0;
  });
  const listRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!listRef.current) return;
    const item = listRef.current.children[selectedIndex] as HTMLElement | undefined;
    item?.focus();
    item?.scrollIntoView({ block: 'nearest' });
  }, [selectedIndex]);

  const pick = useCallback(
    (value: string) => {
      onSelect(value);
      onClose();
    },
    [onSelect, onClose],
  );

  // Escape is Modal's job; the arrow keys are ours.
  const onKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (!open) return;
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setSelectedIndex((i) => Math.min(i + 1, allOptions.length - 1));
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setSelectedIndex((i) => Math.max(i - 1, 0));
      }
    },
    [open, allOptions.length],
  );

  useEffect(() => {
    if (!open) return;
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, [open, onKeyDown]);

  if (!open || options.length === 0) return null;

  // The effective current value — empty string matches the "default" row.
  const effectiveCurrent = current || DEFAULT_VALUE;

  return createPortal(
    <Modal
      label="Reasoning level"
      onClose={onClose}
      backdropClassName="oc-cmd-backdrop"
      dialogClassName="oc-cmd-palette oc-reasoning-picker"
    >
      <div className="oc-cmd-input-wrap">
        <span className="oc-reasoning-picker-title">Reasoning level</span>
        <kbd className="oc-cmd-kbd">ESC</kbd>
      </div>
      <div className="oc-cmd-results" ref={listRef}>
        {allOptions.map((opt, i) => {
          const label = opt || DEFAULT_LABEL;
          const isActive = opt === effectiveCurrent;
          return (
            <button
              type="button"
              key={label}
              className={`oc-cmd-item oc-reasoning-picker-row${i === selectedIndex ? ' oc-cmd-item--selected' : ''}`}
              aria-current={isActive}
              onClick={() => pick(opt)}
              onMouseEnter={() => setSelectedIndex(i)}
            >
              <span
                className="oc-reasoning-picker-check"
                aria-hidden="true"
                data-active={isActive ? 'true' : 'false'}
              >
                {isActive ? <i className="bi bi-check2" /> : null}
              </span>
              <span className="oc-cmd-title">{label}</span>
            </button>
          );
        })}
      </div>
    </Modal>,
    document.body,
  );
}
