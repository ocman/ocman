// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ChartCard, ChartSkeletons } from './shared';

describe('ChartCard', () => {
  it('renders the shared chart structure and custom spacing', () => {
    render(<ChartCard title="Usage" style={{ marginBottom: 24 }}>chart</ChartCard>);

    const card = screen.getByRole('heading', { name: 'Usage' }).parentElement!;
    expect(card).toHaveClass('chart-card', 'metrics-chart-card');
    expect(card).toHaveStyle({ marginBottom: '24px' });
    expect(screen.getByText('chart')).toHaveClass('metrics-chart-body');
  });
});

describe('ChartSkeletons', () => {
  it('renders the requested number of chart placeholders', () => {
    render(<ChartSkeletons cards={3} />);
    expect(screen.getByRole('status', { name: 'Loading charts' }).children).toHaveLength(3);
  });
});
