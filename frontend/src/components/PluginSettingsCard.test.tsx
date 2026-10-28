// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import type { PluginRegistration } from '../lib/plugins';
import { PluginSettingsCard } from './PluginSettingsCard';

const plugin = (ui: boolean): PluginRegistration => ({
  ownerId: 'local', approval: 'fp', checksum: 'abc', enabled: false, removed: false, grants: [],
  description: { id: 'org.example.catalog', name: 'Catalog', version: '1', scope: 'owner', requestedGrants: ui ? ['ui.app'] : [],
    ...(ui ? { ui: { sdk: '^1.0.0', exposes: { index: { sha256: 'f'.repeat(64), size: 10 } } } } : {}) },
  configuration: { values: {}, secrets: {} }, health: { status: 'disabled', restartCount: 0 },
});

it('discloses that approving in-app UI runs code with the ocman session', () => {
  render(<PluginSettingsCard plugin={plugin(true)} owner="local" refresh={vi.fn()} />);
  expect(screen.getByText(/@ocman\/plugin-ui \^1\.0\.0/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Enable' }));
  const note = screen.getByRole('note', { name: 'Plugin interface disclosure' });
  expect(note).toHaveTextContent('This plugin runs UI code with your ocman session.');
  expect(note).toHaveTextContent('f'.repeat(64));
  expect(note).not.toHaveTextContent('remote owners');
});

it('explains that remote owners keep declarative views', () => {
  render(<PluginSettingsCard plugin={plugin(true)} owner="machine" refresh={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Enable' }));
  expect(screen.getByRole('note')).toHaveTextContent('Plugins on remote owners keep their declarative views.');
});

it('shows no disclosure for plugins without UI', () => {
  render(<PluginSettingsCard plugin={plugin(false)} owner="local" refresh={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Enable' }));
  expect(screen.queryByRole('note')).toBeNull();
});
