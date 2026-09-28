// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { ModelSelect } from './ModelSelect';

vi.mock('./ModelLogo', () => ({ ModelLabel: ({ children }: { children: React.ReactNode }) => children }));

const entries = [
	{ provider: 'openai', model: 'gpt-opus-mini', isAvailable: true },
	{ provider: 'anthropic', model: 'claude-opus-4', isAvailable: true, isFavorite: true },
	{ provider: 'openai', model: 'gpt-5', isAvailable: true, recentRank: 1 },
];

it('groups rich entries into the picker sections', async () => {
	const user = userEvent.setup();
	render(<ModelSelect value="" models={[]} modelEntries={entries} onChange={vi.fn()} />);
	await user.click(screen.getByRole('combobox', { name: 'Model' }));
	expect(screen.getByText('Favorites')).toBeInTheDocument();
	expect(screen.getByText('Recent')).toBeInTheDocument();
	expect(screen.getByText('All models')).toBeInTheDocument();
});

it('ranks search results like the picker, pinning favorites first', async () => {
	const user = userEvent.setup();
	const onChange = vi.fn();
	render(<ModelSelect value="" models={[]} modelEntries={entries} onChange={onChange} />);
	await user.click(screen.getByRole('combobox', { name: 'Model' }));
	await user.keyboard('opus');
	const options = screen.getAllByRole('option');
	expect(options.map((o) => o.textContent)).toEqual(['anthropic/claude-opus-4', 'openai/gpt-opus-mini']);
	expect(screen.queryByText('Favorites')).not.toBeInTheDocument();
	await user.click(options[0]);
	expect(onChange).toHaveBeenCalledWith('anthropic/claude-opus-4');
});

it('keeps an unlisted value selectable and offers the default', async () => {
	const user = userEvent.setup();
	const onChange = vi.fn();
	render(<ModelSelect value="custom/model" models={['openai/gpt-5']} defaultLabel="Runtime default" onChange={onChange} />);
	expect(screen.getByRole('combobox', { name: 'Model' })).toHaveTextContent('custom/model');
	await user.click(screen.getByRole('combobox', { name: 'Model' }));
	expect(screen.getByRole('option', { name: 'custom/model' })).toBeInTheDocument();
	await user.click(screen.getByRole('option', { name: 'Runtime default' }));
	expect(onChange).toHaveBeenCalledWith('');
});
