// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ProjectLabel } from './ProjectLabel';

describe('ProjectLabel', () => {
  it('shows a short path while preserving the full path', () => {
    render(<ProjectLabel path="/Users/dries/src/ocman" className="project" />);

    expect(screen.getByText('src/ocman')).toHaveAttribute('title', '/Users/dries/src/ocman');
    expect(screen.getByText('src/ocman')).toHaveClass('project');
  });

  it('shows a fallback for an unavailable path', () => {
    render(<ProjectLabel fallback="Unknown project" />);

    expect(screen.getByText('Unknown project')).not.toHaveAttribute('title');
  });
});
