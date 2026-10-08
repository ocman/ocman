import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { useClickOutside } from '../lib/useClickOutside';
import { fuzzyMatch } from '../lib/format';
import { Button } from './Control';
import './SearchSelect.css';

export interface SearchSelectOption {
  value: string;
  label: string;
  displayLabel?: ReactNode;
  icon?: ReactNode;
  // Header shown above the first option of each section while not searching.
  section?: string;
  disabled?: boolean;
}

interface SearchSelectProps {
  value: string;
  options: SearchSelectOption[];
  ariaLabel: string;
  placeholder: string;
  searchLabel: string;
  disabled?: boolean;
  className?: string;
  title?: string;
  onChange: (value: string) => void;
  // Custom ranking for a non-empty query; defaults to fuzzy label matching.
  search?: (query: string) => SearchSelectOption[];
}

// ponytail: fixed render cap keeps huge catalogs (8k+ models) instant; virtualize if scrolling past it matters.
const MAX_VISIBLE = 200;

export function SearchSelect({
  value,
  options,
  ariaLabel,
  placeholder,
  searchLabel,
  disabled,
  className,
  title,
  onChange,
  search: searchOptions,
}: SearchSelectProps) {
  const id = useId();
  const root = useRef<HTMLSpanElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const [menuOpen, setOpen] = useState(false);
  // A control disabled while open (e.g. a busy lock) must not stay selectable.
  const open = menuOpen && !disabled;
  if (disabled && menuOpen) setOpen(false);
  const [query, setQuery] = useState('');
  const visible = !query.trim() ? options
    : searchOptions ? searchOptions(query)
    : options.filter((option) => fuzzyMatch(query, `${option.label} ${option.value}`));
  const shown = visible.slice(0, MAX_VISIBLE);

  useClickOutside(root, open, () => setOpen(false));

  useEffect(() => {
    if (!open) return;
    search.current?.focus();
  }, [open]);

  const selected = options.find((option) => option.value === value);

  return (
    <span className={className ? `oc-search-select ${className}` : 'oc-search-select'} ref={root} onKeyDown={(event) => {
      if (event.key !== 'Escape' || !open) return;
      event.preventDefault();
      event.stopPropagation();
      setOpen(false);
      trigger.current?.focus();
    }}>
      <Button
        ref={trigger}
        type="button"
        role="combobox"
        aria-label={ariaLabel}
        title={title}
        aria-expanded={open}
        aria-controls={id}
        disabled={disabled}
        onClick={() => {
          setQuery('');
          setOpen((current) => !current);
        }}
      >
        <span className="oc-search-select-label">{selected?.icon}<span className="oc-search-select-text">{selected?.displayLabel ?? selected?.label ?? (value || placeholder)}</span></span>
        <i className="bi bi-chevron-down" aria-hidden="true" />
      </Button>
      {open && (
        <span className="oc-search-select-menu">
          <input
            ref={search}
            className="oc-field oc-field--search"
            aria-label={searchLabel}
            placeholder={searchLabel}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => { if (event.key === 'Enter') event.preventDefault(); }}
          />
          <span id={id} role="listbox">
            {shown.map((option, index) => [
              !query.trim() && option.section && option.section !== shown[index - 1]?.section
                && <span key={`h:${option.section}`} role="presentation" className="oc-search-select-header">{option.section}</span>,
              <button
                type="button"
                role="option"
                aria-selected={option.value === value}
                disabled={option.disabled}
                key={option.value}
                onClick={() => {
                  if (disabled || option.disabled) return;
                  onChange(option.value);
                  setOpen(false);
                }}
              >
                <span className="oc-search-select-label">{option.icon}{option.displayLabel ?? option.label}</span>
              </button>,
            ])}
            {visible.length === 0 && <small>No matches</small>}
            {visible.length > MAX_VISIBLE && <small>Type to search {visible.length - MAX_VISIBLE} more…</small>}
          </span>
        </span>
      )}
    </span>
  );
}
