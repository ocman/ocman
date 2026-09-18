// @vitest-environment jsdom
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { Composer } from './Composer';
import { api } from '../../lib/api';
import { PermissionModeLock } from '../PermissionModeLock';

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => vi.restoreAllMocks());

it('returns focus to the composer after filtering and selecting a model with Enter', async () => {
  const user = userEvent.setup();
  const onModelChange = vi.fn();
  render(<Composer isRunning={false} models={['anthropic/claude', 'openai/gpt']} onModelChange={onModelChange} />);
  const input = screen.getByRole('textbox');
  await waitFor(() => expect(input).toHaveFocus());

  await user.click(screen.getByTitle('Model (click to change)'));
  const search = screen.getByRole('combobox');
  await waitFor(() => expect(search).toHaveFocus());
  await user.type(search, 'claude');
  await user.keyboard('{Enter}');

  expect(onModelChange).toHaveBeenCalledWith('anthropic/claude');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(input).toHaveFocus();
  await user.keyboard('Next prompt');
  expect(input).toHaveValue('Next prompt');
});

it.each(['agent', 'reasoning', 'skills', 'routines', 'permission'])(
  'returns focus after selecting from the %s menu', async (menu) => {
    const user = userEvent.setup();
    const changed = vi.fn();
    vi.spyOn(api, 'commands').mockResolvedValue([{ name: 'review', description: 'Review changes', source: 'skill' }]);
    vi.spyOn(api.routines, 'list').mockResolvedValue([{ id: 'review', name: 'Review', prompt: 'Review changes' }] as Awaited<ReturnType<typeof api.routines.list>>);
    vi.spyOn(api, 'getPermissionRules').mockResolvedValue({ rules: [] });
    vi.spyOn(api, 'setPermissionRules').mockResolvedValue(undefined);
    render(<Composer
      isRunning={false}
      sessionId={`focus-${menu}`}
      agents={[{ name: 'build', mode: 'primary' }, { name: 'plan', mode: 'primary' }]}
      selectedModel="openai/gpt"
      modelEntries={[{ provider: 'openai', model: 'gpt', isAvailable: true, reasoning: ['high'] }]}
      onAgentChange={changed}
      onReasoningChange={changed}
      permissionControl={<PermissionModeLock sessionId="focus-test" />}
    />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).toHaveFocus());

    if (menu === 'agent') await user.click(screen.getByTitle('Agent (click to change)'));
    else if (menu === 'reasoning') await user.click(screen.getByTitle(/Reasoning level/));
    else if (menu === 'permission') await user.click(await screen.findByLabelText('Permission mode: Default'));
    else await user.keyboard(`/${menu}{Enter}`);

    if (menu === 'reasoning') {
      await waitFor(() => expect(screen.getByRole('listbox')).toHaveFocus());
      await user.keyboard('{ArrowDown}{Enter}');
      expect(changed).toHaveBeenCalledWith('high');
    } else {
      const search = screen.getByRole('combobox');
      await waitFor(() => expect(search).toHaveFocus());
      await user.type(search, menu === 'agent' || menu === 'permission' ? 'plan' : 'review');
      await screen.findByRole('option', { selected: true });
      await user.keyboard('{Enter}');
    }

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(input).toHaveFocus();
    const prefix = menu === 'skills' ? '/review ' : menu === 'routines' ? 'Review changes' : '';
    expect(input).toHaveValue(prefix);
    await user.keyboard('Next prompt');
    expect(input).toHaveValue(`${prefix}Next prompt`);
  },
);

it.each(['model', 'agent', 'reasoning', 'skills', 'routines', 'help', 'permission'])(
  'returns focus after dismissing the %s menu with Escape', async (menu) => {
    const user = userEvent.setup();
    vi.spyOn(api, 'commands').mockResolvedValue([{ name: 'review', source: 'skill' }]);
    vi.spyOn(api.routines, 'list').mockResolvedValue([]);
    vi.spyOn(api, 'getPermissionRules').mockResolvedValue({ rules: [] });
    render(<Composer
      isRunning={false}
      sessionId={`dismiss-${menu}`}
      models={['openai/gpt']}
      selectedModel="openai/gpt"
      modelEntries={[{ provider: 'openai', model: 'gpt', isAvailable: true, reasoning: ['high'] }]}
      permissionControl={<PermissionModeLock sessionId="dismiss-test" />}
    />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).toHaveFocus());
    if (menu === 'model') await user.click(screen.getByTitle('Model (click to change)'));
    else if (menu === 'agent') await user.click(screen.getByTitle('Agent (click to change)'));
    else if (menu === 'reasoning') await user.click(screen.getByTitle(/Reasoning level/));
    else if (menu === 'permission') await user.click(await screen.findByLabelText('Permission mode: Default'));
    else await user.keyboard(`/${menu}{Enter}`);

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(input).toHaveFocus();
    await user.keyboard('Next prompt');
    expect(input).toHaveValue('Next prompt');
  },
);

it.each(['Cancel', 'Confirm'])('returns focus after %s on a permission confirmation', async (action) => {
  const user = userEvent.setup();
  vi.spyOn(api, 'getPermissionRules').mockResolvedValue({ rules: [] });
  const save = vi.spyOn(api, 'setPermissionRules').mockResolvedValue(undefined);
  render(<Composer isRunning={false} permissionControl={<PermissionModeLock sessionId="confirmation" />} />);
  const input = screen.getByRole('textbox');
  await waitFor(() => expect(input).toHaveFocus());
  await user.click(await screen.findByLabelText('Permission mode: Default'));
  await user.click(screen.getByRole('option', { name: /YOLO/ }));
  const cancel = screen.getByRole('button', { name: 'Cancel' });
  expect(cancel).toHaveFocus();
  expect(save).not.toHaveBeenCalled();

  await user.click(screen.getByRole('button', { name: action }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(input).toHaveFocus();
  expect(save).toHaveBeenCalledTimes(action === 'Confirm' ? 1 : 0);
  await user.keyboard('Next prompt');
  expect(input).toHaveValue('Next prompt');
});
