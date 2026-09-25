// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Pagination } from './Pagination';

describe('Pagination', () => {
  it.each([
    [true, false], [false, false], [false, true], [true, true],
  ])('respects caller boundaries: previous=%s next=%s', (previousDisabled, nextDisabled) => {
    const onPrevious = vi.fn();
    const onNext = vi.fn();
    render(
      <Pagination previousDisabled={previousDisabled} nextDisabled={nextDisabled} onPrevious={onPrevious} onNext={onNext}>
        <span>Page supplied by caller</span>
      </Pagination>,
    );
    expect(screen.getByRole('group', { name: 'Pagination' })).toHaveTextContent('Page supplied by caller');
    const previous = screen.getByRole('button', { name: 'Prev' });
    const next = screen.getByRole('button', { name: 'Next' });
    expect(previous).toHaveProperty('disabled', previousDisabled);
    expect(next).toHaveProperty('disabled', nextDisabled);
    fireEvent.click(previous);
    fireEvent.click(next);
    expect(onPrevious).toHaveBeenCalledTimes(previousDisabled ? 0 : 1);
    expect(onNext).toHaveBeenCalledTimes(nextDisabled ? 0 : 1);
    expect(screen.getByText('Page supplied by caller')).toBeInTheDocument();
  });

  it('preserves custom labels and hooks without submitting an enclosing form', () => {
    const submit = vi.fn((event: React.FormEvent) => event.preventDefault());
    const onPrevious = vi.fn();
    const onNext = vi.fn();
    render(
      <form onSubmit={submit}>
        <Pagination className="caller-pagination" previousLabel="‹ Prev" nextLabel="Next ›"
          previousTestId="previous" nextTestId="next" previousDisabled={false} nextDisabled={false}
          onPrevious={onPrevious} onNext={onNext}>
          <span>page 2</span>
        </Pagination>
      </form>,
    );
    expect(screen.getByRole('group', { name: 'Pagination' })).toHaveClass('caller-pagination');
    expect(screen.getByTestId('previous')).toHaveAccessibleName('‹ Prev');
    expect(screen.getByTestId('next')).toHaveAccessibleName('Next ›');
    fireEvent.click(screen.getByTestId('previous'));
    fireEvent.click(screen.getByTestId('next'));
    expect(onPrevious).toHaveBeenCalledOnce();
    expect(onNext).toHaveBeenCalledOnce();
    expect(submit).not.toHaveBeenCalled();
  });
});
