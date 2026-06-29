import { useEffect, useId, useState } from 'react';
import { api } from '../lib/api';

interface ModelSelectProps {
  value: string;
  onChange: (value: string) => void;
  // Scope the suggested model history to a project directory.
  directory?: string;
}

/**
 * ModelSelect is a free-text input with a filter-as-you-type dropdown of
 * previously-used `provider/model` values (native <datalist>). Typing a
 * model not in the list is still allowed, matching the prior plain input.
 */
export function ModelSelect({ value, onChange, directory }: ModelSelectProps) {
  const listId = useId();
  const [options, setOptions] = useState<string[]>([]);

  useEffect(() => {
    const controller = new AbortController();
    api
      .models(directory ? { dir: directory } : undefined, controller.signal)
      .then((rows) =>
        setOptions(
          Array.from(new Set(rows.map((m) => `${m.provider}/${m.model}`))).sort(),
        ),
      )
      .catch(() => { /* keep input usable as free text on failure */ });
    return () => controller.abort();
  }, [directory]);

  return (
    <>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="provider/model"
        list={listId}
      />
      <datalist id={listId}>
        {options.map((o) => (
          <option key={o} value={o} />
        ))}
      </datalist>
    </>
  );
}
