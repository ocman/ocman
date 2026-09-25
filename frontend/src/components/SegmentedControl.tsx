import { useId } from 'react';
import './Control.css';
import './SegmentedControl.css';

interface Option<T> {
  value: T;
  label: string;
  icon?: string;
}

interface SegmentedControlProps<T> {
  label: string;
  options: readonly Option<T>[];
  value: T;
  onChange: (value: T) => void;
  compact?: boolean;
}

export function SegmentedControl<T extends string | number>({ label, options, value, onChange, compact = false }: SegmentedControlProps<T>) {
  const name = useId();
  return (
    <div className="oc-segmented-control" role="radiogroup" aria-label={label}>
      {options.map((option) => {
        const selected = value === option.value;
        return (
          <label key={option.value} className="oc-segmented-option">
            <input type="radio" name={name} value={option.value} aria-label={option.label} title={option.label} checked={selected} onChange={() => onChange(option.value)} />
            <span className={`oc-button oc-button--small oc-button--${selected ? 'accent' : 'default'}`}>
              {option.icon && <i className={`bi ${option.icon}`} aria-hidden="true" />}
              {(!compact || !option.icon || selected) && <span>{option.label}</span>}
            </span>
          </label>
        );
      })}
    </div>
  );
}
