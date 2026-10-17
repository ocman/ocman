// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { BackgroundTasksNotice } from './BackgroundTasksNotice';

it('shows a quiet count only while tasks are running', () => {
  const { rerender } = render(<BackgroundTasksNotice count={0} />);
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  rerender(<BackgroundTasksNotice count={1} />);
  expect(screen.getByRole('status')).toHaveTextContent('1 background task running');
  rerender(<BackgroundTasksNotice count={3} />);
  expect(screen.getByRole('status')).toHaveTextContent('3 background tasks running');
  rerender(<BackgroundTasksNotice count={0} />);
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
});
