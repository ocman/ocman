// @vitest-environment jsdom
import { act, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { ArtifactPreview } from './ArtifactPreview';

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

it('announces loading and errors, then clears the error for a different preview', async () => {
  let finish!: (response: Response) => void;
  const fetchPreview = vi.fn(() => new Promise<Response>(resolve => { finish = resolve; }));
  vi.stubGlobal('fetch', fetchPreview);
  vi.spyOn(console, 'warn').mockImplementation(() => {});
  const { rerender } = render(<ArtifactPreview item={{ kind: 'file', name: 'report.txt', mime: 'text/plain', url: '/report.txt' }} />);
  expect(screen.getByRole('status')).toHaveTextContent('Loading preview');
  await act(async () => finish(new Response('Unavailable', { status: 503 })));
  expect(await screen.findByRole('alert')).toHaveTextContent('Preview unavailable');
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  rerender(<ArtifactPreview item={{ kind: 'file', name: 'next.txt', mime: 'text/plain', url: '/next.txt' }} />);
  expect(screen.getByRole('status')).toHaveTextContent('Loading preview');
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  await act(async () => finish(new Response('Next preview loaded')));
  expect(await screen.findByTestId('artifact-preview-text')).toHaveTextContent('Next preview loaded');
});
