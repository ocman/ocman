// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { Composer } from './Composer';

const caps = vi.hoisted(() => ({ agentCatalog: true }));
vi.mock('../../lib/useCapabilities', () => ({
  useMultiPlatform: () => false,
  useMultiHost: () => false,
  usePlatformCapabilities: () => caps,
}));
beforeEach(() => { caps.agentCatalog = true; });

it.each([
  ['opencode', 'OpenCode'],
  ['claudecode', 'Claude Code'],
  ['r-owner:claudecode', 'Claude Code (remote machine)'],
])('identifies %s before the agent selector even with one platform', (platform, label) => {
  render(<Composer platform={platform} isRunning={false} agentsLoaded activeAgent="build" />);
  const icon = screen.getByLabelText(label);
  const agent = screen.getByRole('button', { name: 'build' });
  expect(icon.compareDocumentPosition(agent) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(icon.querySelector('img')).toHaveAttribute('src', `/platform-${platform.split(':').pop()}.svg`);
});

it('keeps the platform icon but removes unsupported agent selection and Tab cycling', () => {
  caps.agentCatalog = false;
  const onAgentChange = vi.fn();
  render(<Composer platform="claudecode" isRunning={false} agentsLoaded activeAgent="build"
    agents={[{ name: 'reviewer', mode: 'subagent' }]} onAgentChange={onAgentChange} />);
  expect(screen.getByLabelText('Claude Code')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'build' })).not.toBeInTheDocument();
  const input = screen.getByRole('textbox');
  fireEvent.keyDown(input, { key: 'Tab' });
  expect(onAgentChange).not.toHaveBeenCalled();
  fireEvent.input(input, { target: { value: '/agent' } });
  expect(screen.queryByRole('option', { name: /agent/i })).not.toBeInTheDocument();
});

it('opens named model tabs even before the selected account catalog is ready', async () => {
  Element.prototype.scrollIntoView = vi.fn();
  const onChange = vi.fn();
  render(<Composer isRunning={false} newConversation agentsLoaded={false} modelPicker={{
    tabs: { label: 'Model platforms', value: 'work', items: [
      { value: 'work', label: 'Work' }, { value: 'personal', label: 'Personal' },
    ], onChange },
    status: <div role="status">Loading models...</div>,
  }} />);
  fireEvent.click(screen.getByRole('button', { name: 'Model' }));
  expect(await screen.findByRole('tab', { name: 'Work' })).toHaveAttribute('aria-selected', 'true');
  expect(screen.getByText('Loading models...')).toBeVisible();
  expect(screen.queryByText('No models found')).not.toBeInTheDocument();
  fireEvent.mouseDown(screen.getByRole('tab', { name: 'Personal' }), { button: 0 });
  expect(onChange).toHaveBeenCalledWith('personal');
});

it('keeps selector skeletons while loading a catalog without named platform tabs', () => {
  render(<Composer isRunning={false} newConversation agentsLoaded={false}
    modelPicker={{ status: <div role="status">Loading models...</div> }} />);
  expect(screen.getByRole('status', { name: 'Loading agents and models' })).toHaveAttribute('aria-busy', 'true');
  expect(screen.queryByRole('button', { name: 'Model' })).not.toBeInTheDocument();
  expect(screen.getByRole('textbox')).toBeEnabled();
});
