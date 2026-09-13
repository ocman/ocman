// @vitest-environment jsdom
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { api } from '../lib/api';
import { ModelSelect } from './ModelSelect';

vi.mock('../lib/api', () => ({ api: { models: vi.fn() } }));
vi.mock('./ModelLogo', () => ({ ModelLogo: () => null }));

afterEach(() => vi.clearAllMocks());

it('loads unique project models while retaining the current value', async () => {
	const user = userEvent.setup();
	let signal: AbortSignal | undefined;
	vi.mocked(api.models).mockImplementation(async (_query, requestSignal) => {
		signal = requestSignal;
		return [{ provider: 'openai', model: 'gpt-5' }, { provider: 'openai', model: 'gpt-5' }] as never;
	});
	const onChange = vi.fn();
	const view = render(<ModelSelect value="custom/model" directory="/repo" onChange={onChange} />);

	await waitFor(() => expect(api.models).toHaveBeenCalledWith({ dir: '/repo' }, expect.any(AbortSignal)));
	await user.click(screen.getByRole('combobox', { name: 'Model' }));
	await screen.findByRole('option', { name: 'openai/gpt-5' });
	expect(screen.getAllByRole('option', { name: 'openai/gpt-5' })).toHaveLength(1);
	expect(screen.getByRole('option', { name: 'custom/model' })).toBeInTheDocument();
	await user.click(screen.getByRole('option', { name: 'openai/gpt-5' }));
	expect(onChange).toHaveBeenCalledWith('openai/gpt-5');
	view.unmount();
	expect(signal?.aborted).toBe(true);
});

it('remains usable when model history fails', async () => {
	const user = userEvent.setup();
	const onChange = vi.fn();
	vi.mocked(api.models).mockRejectedValue(new Error('offline'));
	render(<ModelSelect value="custom/model" onChange={onChange} />);
	await waitFor(() => expect(api.models).toHaveBeenCalledWith(undefined, expect.any(AbortSignal)));
	await user.click(screen.getByRole('combobox', { name: 'Model' }));
	await user.click(screen.getByRole('option', { name: 'Default model' }));
	expect(onChange).toHaveBeenCalledWith('');
});
