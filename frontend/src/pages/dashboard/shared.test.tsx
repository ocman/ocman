// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ChartCard, ChartSkeletons, MetricCardsSkeleton } from './shared';

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
    render(<ChartSkeletons labels={['Loading one', 'Loading two', 'Loading three']} />);
    expect(screen.getAllByRole('status')).toHaveLength(3);
  });

  it('renders metric-card placeholders', () => {
    render(<MetricCardsSkeleton cards={3} label="Loading summary" />);
    expect(screen.getByRole('status', { name: 'Loading summary' }).children).toHaveLength(3);
  });
});
