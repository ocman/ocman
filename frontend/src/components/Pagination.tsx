import type { ReactNode } from 'react';
import { Button, ButtonGroup } from './Control';
import './Pagination.css';

/** Presentation only: callers own page indexing, boundaries, and visibility. */
export function Pagination({
  children, className = '', previousLabel = 'Prev', nextLabel = 'Next',
  previousDisabled, nextDisabled, onPrevious, onNext, previousTestId, nextTestId,
}: {
  children: ReactNode;
  className?: string;
  previousLabel?: string;
  nextLabel?: string;
  previousDisabled: boolean;
  nextDisabled: boolean;
  onPrevious: () => void;
  onNext: () => void;
  previousTestId?: string;
  nextTestId?: string;
}) {
  return (
    <ButtonGroup label="Pagination" className={`oc-pagination ${className}`}>
      <Button type="button" size="compact" disabled={previousDisabled} onClick={onPrevious} data-testid={previousTestId}>
        {previousLabel}
      </Button>
      {children}
      <Button type="button" size="compact" disabled={nextDisabled} onClick={onNext} data-testid={nextTestId}>
        {nextLabel}
      </Button>
    </ButtonGroup>
  );
}
