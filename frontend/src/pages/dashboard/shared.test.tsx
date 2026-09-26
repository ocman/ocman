// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ChartCard, ChartSkeletons, MetricCardsSkeleton, MetricsPagination } from './shared';

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

describe('MetricsPagination', () => {
  it.each([0, 9, 10])('hides controls for %s items fitting on one page', (total) => {
    const { container } = render(<MetricsPagination page={0} pageSize={10} total={total} onChange={vi.fn()} />);
    expect(container).toBeEmptyDOMElement();
  });

  it.each([
    { page: 0, total: 21, text: 'Page 1 / 3', previousDisabled: true, nextDisabled: false, calls: [1] },
    { page: 1, total: 21, text: 'Page 2 / 3', previousDisabled: false, nextDisabled: false, calls: [0, 2] },
    { page: 2, total: 21, text: 'Page 3 / 3', previousDisabled: false, nextDisabled: true, calls: [1] },
    { page: 1, total: 20, text: 'Page 2 / 2', previousDisabled: false, nextDisabled: true, calls: [0] },
  ])('keeps zero-based callbacks and boundaries for $text with $total items', ({ page, total, text, previousDisabled, nextDisabled, calls }) => {
    const onChange = vi.fn();
    render(<MetricsPagination page={page} pageSize={10} total={total} onChange={onChange} />);
    expect(screen.getByRole('group', { name: 'Pagination' })).toHaveTextContent(text);
    const previous = screen.getByRole('button', { name: 'Prev' });
    const next = screen.getByRole('button', { name: 'Next' });
    expect(previous).toHaveProperty('disabled', previousDisabled);
    expect(next).toHaveProperty('disabled', nextDisabled);
    fireEvent.click(previous);
    fireEvent.click(next);
    expect(onChange.mock.calls).toEqual(calls.map((value) => [value]));
    expect(screen.getByText(text)).toBeInTheDocument();
  });
});
