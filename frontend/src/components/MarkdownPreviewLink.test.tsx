// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MarkdownContent } from './assistant/MarkdownText';
import { previewAuthChanged, PreviewOwnerContext } from '../lib/previews';

const url = 'https://example.com/article';
let requests: { text: string; openGraph?: boolean; owner: string }[];
let results: unknown[];

beforeEach(() => {
  vi.useFakeTimers();
  requests = [];
  results = [{ provider: 'opengraph', kind: 'page', id: 'example.com', url, title: 'A useful article', description: 'The description', image: 'https://example.com/card.png', imageAlt: 'Article cover', meta: ['Example'], state: 'ok' }];
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    if (input.includes('/providers')) return new Response(JSON.stringify({ providers: [], rules: [] }));
    requests.push({ ...JSON.parse(init?.body as string), owner: input });
    return new Response(JSON.stringify({ previews: results }));
  }));
  previewAuthChanged();
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); });

async function advance(ms: number) {
  for (let remaining = ms; remaining > 0; remaining -= 100) {
    await act(async () => { await vi.advanceTimersByTimeAsync(Math.min(100, remaining)); });
  }
}
function link() {
  render(<MarkdownContent text={`Read [the article](${url}).`} />);
  return screen.getByRole('link', { name: 'the article' });
}

it('waits for hover before resolving Open Graph and renders the card in a portal', async () => {
  const anchor = link();
  expect(anchor).toHaveAttribute('href', url);
  expect(anchor).toHaveAttribute('target', '_blank');
  expect(requests).toHaveLength(0);
  fireEvent.mouseEnter(anchor);
  await advance(499);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(requests).toHaveLength(0);
  await advance(1);
  await advance(300);
  const dialog = screen.getByRole('dialog', { name: 'Link preview' });
  expect(dialog.parentElement).toBe(document.body);
  expect(within(dialog).getByText('A useful article')).toBeInTheDocument();
  expect(within(dialog).getByText('The description')).toBeInTheDocument();
  const image = within(dialog).getByRole('img', { name: 'Article cover' });
  expect(image).toHaveAttribute('src', '/api/previews/image?url=https%3A%2F%2Fexample.com%2Fcard.png');
  fireEvent.error(image);
  expect(image).not.toBeVisible();
  expect(requests[0]).toMatchObject({ text: url, openGraph: true });
});

it('cancels brief hovers and pending work on unmount', async () => {
  const anchor = link();
  fireEvent.mouseEnter(anchor);
  await advance(200);
  fireEvent.mouseLeave(anchor);
  await advance(1000);
  expect(requests).toHaveLength(0);
  fireEvent.mouseEnter(anchor);
  cleanup();
  await advance(1000);
  expect(requests).toHaveLength(0);
});

it('keeps the preview usable while crossing to the card and closes after leaving', async () => {
  const anchor = link();
  fireEvent.mouseEnter(anchor);
  await advance(800);
  fireEvent.mouseLeave(anchor);
  const body = screen.getByTestId('provider-preview-card').parentElement!;
  fireEvent.mouseEnter(body);
  await advance(200);
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  fireEvent.mouseLeave(body);
  await advance(150);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('supports keyboard focus, moving into the card, blur and Escape without stealing focus', async () => {
  const anchor = link();
  act(() => anchor.focus());
  await advance(800);
  expect(anchor).toHaveFocus();
  const card = screen.getByTestId('provider-preview-card');
  act(() => card.focus());
  await advance(150);
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  fireEvent.keyDown(document, { key: 'Escape' });
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(anchor).toHaveFocus();
  act(() => anchor.blur());
  await advance(1000);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('closes on scroll or navigation and ignores unsupported links', async () => {
  const anchor = link();
  fireEvent.mouseEnter(anchor);
  await advance(800);
  fireEvent.scroll(window);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  fireEvent.mouseEnter(anchor);
  await advance(800);
  fireEvent.click(anchor);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  cleanup();
  results = [];
  fireEvent.mouseEnter(link());
  await advance(800);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('closes when keyboard focus leaves the preview card', async () => {
  const anchor = link();
  act(() => anchor.focus());
  await advance(800);
  const card = screen.getByTestId('provider-preview-card');
  act(() => card.focus());
  await advance(150);
  act(() => card.blur());
  await advance(150);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('uses the conversation owner and leaves relative and mail links alone', async () => {
  render(<PreviewOwnerContext.Provider value="remote-1"><MarkdownContent text={`[article](${url}) [local](/settings) [mail](mailto:a@example.com)`} /></PreviewOwnerContext.Provider>);
  fireEvent.mouseEnter(screen.getByRole('link', { name: 'local' }));
  fireEvent.mouseEnter(screen.getByRole('link', { name: 'mail' }));
  await advance(1000);
  expect(requests).toHaveLength(0);
  fireEvent.mouseEnter(screen.getByRole('link', { name: 'article' }));
  await advance(800);
  expect(requests[0].owner).toContain('remoteId=remote-1');
});

it('fits above links near the viewport edge and closes on outside clicks or resize', async () => {
  const anchor = link();
  vi.spyOn(anchor, 'getBoundingClientRect').mockReturnValue({ top: 700, bottom: 720, left: 1000 } as DOMRect);
  fireEvent.mouseEnter(anchor);
  await advance(800);
  expect(screen.getByRole('dialog')).toHaveStyle({ bottom: '74px', maxHeight: '560px' });
  fireEvent.mouseDown(document.body);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  fireEvent.mouseEnter(anchor);
  await advance(800);
  fireEvent.resize(window);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('discards a pending preview when a streamed markdown link changes', async () => {
  const { rerender } = render(<MarkdownContent text={`[article](${url})`} />);
  fireEvent.mouseEnter(screen.getByRole('link'));
  await advance(400);
  rerender(<MarkdownContent text="[article](https://another.example/new)" />);
  await advance(1000);
  expect(requests).toHaveLength(0);
});
