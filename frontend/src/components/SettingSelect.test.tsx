// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { SettingSelect, type SettingSelectProps } from './SettingRow';
import { useSettingSave } from '../lib/useSaveStatus';

function Harness(props: Omit<SettingSelectProps, 'save' | 'ariaLabel' | 'placeholder' | 'searchLabel'>) {
  const save = useSettingSave();
  return (
    <SettingSelect
      ariaLabel="model"
      placeholder="Default"
      searchLabel="Search models"
      save={save}
      {...props}
    />
  );
}

const options = [
  { value: '', label: 'Default (anthropic/haiku)' },
  { value: 'openai/gpt', label: 'openai/gpt' },
];

describe('SettingSelect', () => {
  it('saves the picked option', async () => {
    const onSave = vi.fn();
    render(<Harness value="" options={options} onSave={onSave} />);

    fireEvent.click(screen.getByLabelText('model'));
    fireEvent.click(screen.getByRole('option', { name: 'openai/gpt' }));

    await waitFor(() => expect(onSave).toHaveBeenCalledWith('openai/gpt'));
  });

  it('does not save when the same option is picked again', () => {
    const onSave = vi.fn();
    render(<Harness value="openai/gpt" options={options} onSave={onSave} />);

    fireEvent.click(screen.getByLabelText('model'));
    fireEvent.click(screen.getByRole('option', { name: 'openai/gpt' }));

    expect(onSave).not.toHaveBeenCalled();
  });

  it('still shows a value the catalog no longer offers', () => {
    render(<Harness value="gone/model" options={options} onSave={vi.fn()} />);
    expect(screen.getByLabelText('model')).toHaveTextContent('gone/model');
  });
});
