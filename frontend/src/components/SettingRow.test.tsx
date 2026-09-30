// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { SettingRow, SettingToggle, SettingNumber, SettingText } from './SettingRow';
import { useSettingSave } from '../lib/useSaveStatus';

// Wrappers so the control uses the live hook value (state updates re-render).
function ToggleFixture({ onSave }: { onSave: (next: boolean) => void | Promise<unknown> }) {
  const save = useSettingSave();
  return <SettingToggle ariaLabel="t" checked={false} save={save} onSave={onSave} />;
}

function NumberFixture({ onSave }: { onSave: (next: number) => void }) {
  const save = useSettingSave();
  return (
    <SettingNumber ariaLabel="n" unit="days" value={1} parse={(raw) => raw * 24} save={save} onSave={onSave} />
  );
}

describe('SettingToggle', () => {
  it('runs onSave and shows spinner then checkmark', async () => {
    let saved: boolean | null = null;
    render(<ToggleFixture onSave={(next) => { saved = next; }} />);
    fireEvent.click(screen.getByLabelText('t'));
    expect(await screen.findByTestId('save-status-spinner')).toBeTruthy();
    expect(saved).toBe(true);
    await waitFor(() => expect(screen.getByTestId('save-status-saved')).toBeTruthy());
  });

  it('shows the error indicator when onSave rejects (and does not throw)', async () => {
    render(<ToggleFixture onSave={() => Promise.reject(new Error('boom'))} />);
    fireEvent.click(screen.getByLabelText('t'));
    await waitFor(() => expect(screen.getByTestId('save-status-error')).toBeTruthy());
  });
});

describe('SettingNumber', () => {
  it('parses the raw value before saving', async () => {
    let saved: number | null = null;
    render(<NumberFixture onSave={(next) => { saved = next; }} />);
    fireEvent.change(screen.getByLabelText('n'), { target: { value: '2' } });
    await waitFor(() => expect(saved).toBe(48));
  });
});

function TextFixture({ onSave }: { onSave: (next: string) => void }) {
  const save = useSettingSave();
  return <SettingText ariaLabel="x" value="a" save={save} onSave={onSave} />;
}

describe('SettingText', () => {
  it('saves changed values on blur and Enter only', async () => {
    const saved: string[] = [];
    render(<TextFixture onSave={(next) => { saved.push(next); }} />);
    const input = screen.getByLabelText('x');
    fireEvent.blur(input);
    fireEvent.change(input, { target: { value: 'b' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    fireEvent.change(input, { target: { value: 'c' } });
    fireEvent.blur(input);
    await waitFor(() => expect(saved).toEqual(['b', 'c']));
  });
});

describe('SettingRow', () => {
  it('renders catalog copy with a collapsed example and a jump anchor', () => {
    const { container } = render(<SettingRow setting="bell-sound"><span /></SettingRow>);
    expect(screen.getByText('Bell sound')).toBeTruthy();
    expect(screen.getByText(/Play a bell sound/)).toBeTruthy();
    const details = container.querySelector('details');
    expect(details?.open).toBe(false);
    expect(screen.getByText('Example').tagName).toBe('SUMMARY');
    expect(container.querySelector('#setting-bell-sound')).toBeTruthy();
  });

  it('lets live state override the description and adds detail', () => {
    render(<SettingRow setting="bell-sound" desc="Blocked" detail="extra"><span /></SettingRow>);
    expect(screen.getByText('Blocked')).toBeTruthy();
    expect(screen.getByText('extra')).toBeTruthy();
    expect(screen.queryByText(/Play a bell sound/)).toBeNull();
  });
});
