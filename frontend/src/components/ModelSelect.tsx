import { useEffect, useState } from 'react';
import { api } from '../lib/api';
import { SearchSelect, type SearchSelectOption } from './SearchSelect';

interface ModelSelectProps {
  value: string;
  onChange: (value: string) => void;
  // Scope the suggested model history to a project directory.
  directory?: string;
}

/**
 * Searchable model history, optionally scoped to one project.
 */
export function ModelSelect({ value, onChange, directory }: ModelSelectProps) {
  const [options, setOptions] = useState<SearchSelectOption[]>([]);

  useEffect(() => {
    const controller = new AbortController();
    api
      .models(directory ? { dir: directory } : undefined, controller.signal)
      .then((rows) =>
        setOptions(
          Array.from(new Set(rows.map((m) => `${m.provider}/${m.model}`))).map((model) => ({
            value: model,
            label: model,
          })),
        ),
      )
      .catch(() => {
        /* keep input usable as free text on failure */
      });
    return () => controller.abort();
  }, [directory]);

  const current = value && !options.some((option) => option.value === value) ? [{ value, label: value }] : [];
  return (
    <SearchSelect
      value={value}
      options={[{ value: '', label: 'Default model' }, ...current, ...options]}
      ariaLabel="Model"
      placeholder="Default model"
      searchLabel="Search models"
      onChange={onChange}
    />
  );
}
