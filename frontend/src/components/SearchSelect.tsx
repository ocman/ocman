import { useEffect, useId, useMemo, useRef, useState } from 'react';
import Fuse from 'fuse.js';
import './Control.css';
import './SearchSelect.css';

export interface SearchSelectOption {
  value: string;
  label: string;
}

interface SearchSelectProps {
  value: string;
  options: SearchSelectOption[];
  ariaLabel: string;
  placeholder: string;
  searchLabel: string;
  disabled?: boolean;
  onChange: (value: string) => void;
}

export function SearchSelect({
  value,
  options,
  ariaLabel,
  placeholder,
  searchLabel,
  disabled,
  onChange,
}: SearchSelectProps) {
  const id = useId();
  const root = useRef<HTMLDivElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const fuse = useMemo(
    () => new Fuse(options, { keys: ['label', 'value'], threshold: 0.4, ignoreLocation: true }),
    [options],
  );
  const visible = query ? fuse.search(query).map(({ item }) => item) : options;

  useEffect(() => {
    if (!open) return;
    search.current?.focus();
    const close = (event: MouseEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', close);
    return () => document.removeEventListener('mousedown', close);
  }, [open]);

  const selected = options.find((option) => option.value === value);

  return (
    <div className="oc-search-select" ref={root} onKeyDown={(event) => event.key === 'Escape' && setOpen(false)}>
      <button
        type="button"
        role="combobox"
        aria-label={ariaLabel}
        aria-expanded={open}
        aria-controls={id}
        disabled={disabled}
        onClick={() => {
          setQuery('');
          setOpen((current) => !current);
        }}
      >
        <span>{selected?.label ?? (value || placeholder)}</span>
        <i className="bi bi-chevron-down" aria-hidden="true" />
      </button>
      {open && (
        <div className="oc-search-select-menu">
          <input
            ref={search}
            className="oc-field oc-field--search"
            aria-label={searchLabel}
            placeholder={searchLabel}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          <div id={id} role="listbox">
            {visible.map((option) => (
              <button
                type="button"
                role="option"
                aria-selected={option.value === value}
                key={option.value}
                onClick={() => {
                  onChange(option.value);
                  setOpen(false);
                }}
              >
                {option.label}
              </button>
            ))}
            {visible.length === 0 && <small>No matches</small>}
          </div>
        </div>
      )}
    </div>
  );
}
