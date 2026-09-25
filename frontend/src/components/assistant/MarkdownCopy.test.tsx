// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, it, vi } from 'vitest';
import { MarkdownContent } from './MarkdownText';

afterEach(() => vi.restoreAllMocks());

it('only confirms code copying after the clipboard write succeeds', async () => {
  userEvent.setup();
  let finish!: () => void;
  const write = vi.spyOn(navigator.clipboard, 'writeText').mockReturnValue(new Promise<void>((resolve) => { finish = resolve; }));
  render(<MarkdownContent text={'```ts\nconst answer = 42;\n```'} />);
  const copy = screen.getByTitle('Copy code');
  fireEvent.click(copy);
  expect(write).toHaveBeenCalledWith('const answer = 42;\n');
  expect(copy.querySelector('.bi-check2')).toBeNull();
  expect(copy).toBeDisabled();
  await act(async () => finish());
  expect(await screen.findByRole('status')).toHaveTextContent('Copied!');
  expect(copy).toBeEnabled();
});
