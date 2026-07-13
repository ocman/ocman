// @vitest-environment jsdom

import { fireEvent, render, screen } from '@testing-library/react';
import { beforeAll, describe, expect, it, vi } from 'vitest';
import { MovePathDialog, MovePicker } from './MovePicker';

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

describe('MovePicker', () => {
  it('lists the current directory and de-duplicated alternatives', () => {
    render(
      <MovePicker
        open
        currentDirectory="/current"
        directories={['/other-b', '/current', '/other-a', '/other-a']}
        onSelect={vi.fn()}
        onCustom={vi.fn()}
        onClose={vi.fn()}
      />,
    );

    expect(screen.getByText('/current')).toBeInTheDocument();
    expect(screen.getByText('/other-a')).toBeInTheDocument();
    expect(screen.getByText('/other-b')).toBeInTheDocument();
    expect(screen.getAllByText('/other-a')).toHaveLength(1);
    expect(screen.getByText('Choose another directory...')).toBeInTheDocument();
  });

  it('selects a known directory or opens the custom path dialog', () => {
    const onSelect = vi.fn();
    const onCustom = vi.fn();
    render(
      <MovePicker
        open
        currentDirectory="/current"
        directories={['/other']}
        onSelect={onSelect}
        onCustom={onCustom}
        onClose={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByText('/other'));
    expect(onSelect).toHaveBeenCalledWith('/other');

    fireEvent.click(screen.getByText('/current'));
    expect(onSelect).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByText('Choose another directory...'));
    expect(onCustom).toHaveBeenCalledTimes(1);
  });
});

describe('MovePathDialog', () => {
  it('submits a trimmed custom directory', () => {
    const onSelect = vi.fn();
    render(<MovePathDialog onSelect={onSelect} onClose={vi.fn()} />);

    fireEvent.change(screen.getByLabelText('Project directory'), { target: { value: '  /custom/path  ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Move' }));

    expect(onSelect).toHaveBeenCalledWith('/custom/path');
  });
});
