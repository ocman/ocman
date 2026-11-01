// @vitest-environment jsdom
import { afterEach, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ConversationSection } from './SettingsSections';
import { useUiStore } from '../../lib/uiStore';
import { featureSettings } from '../../features';
import { searchSettings } from '../../lib/settingsCatalog';

afterEach(() => useUiStore.setState({ compactToolCalls: false }));

it('registers a searchable Conversation section', async () => {
  const group = featureSettings.find((g) => g.id === 'conversation');
  expect(group).toMatchObject({ label: 'Conversation' });
  // The registry loads the section on first render.
  const { Section } = group!;
  render(<Section />);
  expect(screen.getByRole('status')).toHaveTextContent('Loading…');
  expect(await screen.findByRole('checkbox', { name: 'Compact tool calls' })).toBeInTheDocument();
  expect(searchSettings('compact', { sessions: 'Sessions' })).toContainEqual(
    expect.objectContaining({ id: 'compact-tool-calls', group: 'sessions' }),
  );
});

it('defaults off and persists the compact tool-call preference across hydration', async () => {
  expect(useUiStore.getState().compactToolCalls).toBe(false);
  render(<ConversationSection />);
  const toggle = screen.getByRole('checkbox', { name: 'Compact tool calls' });
  expect(toggle).not.toBeChecked();
  await userEvent.click(toggle);
  expect(useUiStore.getState().compactToolCalls).toBe(true);
  expect(JSON.parse(localStorage.getItem('ocman:ui')!).state.compactToolCalls).toBe(true);
  await useUiStore.persist.rehydrate();
  expect(useUiStore.getState().compactToolCalls).toBe(true);
  await userEvent.click(toggle);
  expect(useUiStore.getState().compactToolCalls).toBe(false);
});
