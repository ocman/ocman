// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { expect, it } from 'vitest';
import type { FactoryEpic } from '../lib/api';
import { FactoryEpicCards } from './FactoryEpicCards';

const epic = (over: Partial<FactoryEpic>) => ({ id: 'e', goal: 'Goal', status: 'open', initialProject: '/repo', progress: { requiredTotal: 4, requiredSucceeded: 1, optionalOpen: 0 }, ...over }) as FactoryEpic;

it('shows open epics as linked cards with brief and progress', () => {
	render(<MemoryRouter><FactoryEpicCards epics={[
		epic({ id: 'ship/1', goal: 'Ship it', brief: 'Deliver the thing' }),
		epic({ id: 'done', goal: 'Finished', status: 'closed' }),
		epic({ id: 'hold', goal: 'On hold', status: 'paused' }),
	]} /></MemoryRouter>);

	const cards = screen.getByRole('region', { name: 'Epics in progress' });
	expect(screen.getAllByRole('link')).toHaveLength(1);
	expect(screen.getByRole('link', { name: /Ship it/ })).toHaveAttribute('href', '/factory/epics/ship%2F1');
	expect(cards).toHaveTextContent('Deliver the thing');
	expect(cards).toHaveTextContent('1/4');
	expect(screen.getByRole('progressbar', { name: 'Ship it required issues done' })).toHaveAttribute('value', '1');
});

it('renders nothing without open epics', () => {
	const { container } = render(<MemoryRouter><FactoryEpicCards epics={[epic({ status: 'closed' })]} /></MemoryRouter>);
	expect(container).toBeEmptyDOMElement();
});
