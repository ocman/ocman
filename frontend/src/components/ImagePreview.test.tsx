// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { MarkdownContent } from './assistant/MarkdownText';
import { ImagePreviewProvider, PreviewImage } from './ImagePreview';

vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} });

function LocationControls() {
  const location = useLocation();
  const navigate = useNavigate();
  return <>
    <output data-testid="url">{location.pathname + location.search + location.hash}</output>
    <button onClick={() => navigate(-1)}>Back</button>
    <button onClick={() => navigate(1)}>Forward</button>
  </>;
}

function Gallery({ extra = false }: { extra?: boolean }) {
  return <ImagePreviewProvider>
    <MarkdownContent text="![First](/first.png)\n\n![Second](/second.png)" />
    {extra && <PreviewImage src="data:image/png;base64,AA" alt="Attachment" />}
  </ImagePreviewProvider>;
}

describe('image preview navigation', () => {
  it('adds a shareable URL when opening a markdown image', async () => {
    render(<MemoryRouter initialEntries={['/session/example?owner=remote#message']}>
      <LocationControls />
      <ImagePreviewProvider><MarkdownContent text="![First](/first.png)" /></ImagePreviewProvider>
    </MemoryRouter>);
    await userEvent.setup().click(screen.getByRole('button', { name: 'Expand First' }));
    expect(screen.getByTestId('url').textContent).toMatch(/image=/);
    expect(within(screen.getByRole('dialog')).getByRole('img')).toHaveAttribute('src', '/first.png');
  });

  it('navigates markdown images and attachments with arrows and buttons, preserving URL context', async () => {
    const user = userEvent.setup();
    render(<MemoryRouter initialEntries={['/session/example?owner=remote#message']}>
      <LocationControls /><Gallery extra />
    </MemoryRouter>);
    await user.click(screen.getByRole('button', { name: 'Expand First' }));
    const firstURL = screen.getByTestId('url').textContent;
    expect(screen.getByRole('button', { name: 'Previous image' })).toBeDisabled();
    await user.keyboard('{ArrowLeft}');
    expect(screen.getByTestId('url')).toHaveTextContent(firstURL!);
    await user.click(screen.getByRole('button', { name: 'Zoom in' }));
    expect(screen.getByText('125%')).toBeInTheDocument();
    await user.keyboard('{ArrowRight}');
    expect(screen.getByRole('dialog', { name: 'Second' })).toBeInTheDocument();
    expect(screen.getByText('100%')).toBeInTheDocument();
    expect(screen.getByText('2 / 3')).toBeInTheDocument();
    expect(screen.getByTestId('url').textContent).not.toBe(firstURL);
    expect(screen.getByTestId('url').textContent).toMatch(/owner=remote&image=[^#]+#message$/);
    await user.click(screen.getByRole('button', { name: 'Next image' }));
    expect(screen.getByRole('dialog', { name: 'Attachment' })).toBeInTheDocument();
    expect(screen.getByTestId('url').textContent?.length).toBeLessThan(100);
    expect(screen.getByRole('button', { name: 'Next image' })).toBeDisabled();
    await user.keyboard('{ArrowRight}');
    expect(screen.getByRole('dialog', { name: 'Attachment' })).toBeInTheDocument();
    await user.keyboard('{ArrowLeft}');
    await user.click(screen.getByRole('button', { name: 'Previous image' }));
    expect(screen.getByTestId('url')).toHaveTextContent(firstURL!);
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByTestId('url')).toHaveTextContent('/session/example?owner=remote#message');
    expect(screen.getByRole('button', { name: 'Expand First' })).toHaveFocus();
  });

  it('reopens a shared link on reload, including images that arrive later', async () => {
    const user = userEvent.setup();
    const first = render(<MemoryRouter initialEntries={['/session/example']}><LocationControls /><Gallery extra /></MemoryRouter>);
    await user.click(screen.getByRole('button', { name: 'Expand Attachment' }));
    const url = screen.getByTestId('url').textContent!;
    first.unmount();
    const second = render(<MemoryRouter initialEntries={[url]}><Gallery /></MemoryRouter>);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    second.rerender(<MemoryRouter initialEntries={[url]}><Gallery extra /></MemoryRouter>);
    expect(await screen.findByRole('dialog', { name: 'Attachment' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Close image' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('supports browser back and forward without adding history for each arrow press', async () => {
    const user = userEvent.setup();
    render(<MemoryRouter initialEntries={['/session/example']}><LocationControls /><Gallery /></MemoryRouter>);
    await user.click(screen.getByRole('button', { name: 'Expand First' }));
    await user.keyboard('{ArrowRight}');
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Forward' }));
    expect(screen.getByRole('dialog', { name: 'Second' })).toBeInTheDocument();
  });

  it('distinguishes repeated image sources and ignores modified arrow shortcuts', async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><LocationControls /><ImagePreviewProvider>
      <PreviewImage src="/same.png" alt="First copy" />
      <PreviewImage src="/same.png" alt="Second copy" />
    </ImagePreviewProvider></MemoryRouter>);
    await user.click(screen.getByRole('button', { name: 'Expand Second copy' }));
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
    expect(screen.getByRole('dialog', { name: 'Second copy' })).toBeInTheDocument();
    await user.keyboard('{Alt>}{ArrowLeft}{/Alt}');
    expect(screen.getByRole('dialog', { name: 'Second copy' })).toBeInTheDocument();
    await user.keyboard('{ArrowLeft}');
    expect(screen.getByRole('dialog', { name: 'First copy' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('dialog').parentElement!);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('ignores unknown IDs without fetching arbitrary image URLs', () => {
    render(<MemoryRouter initialEntries={['/?image=https://example.com/private.png']}><Gallery /></MemoryRouter>);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getAllByRole('img')).toHaveLength(2);
  });

  it('keeps previews usable outside a router', async () => {
    const user = userEvent.setup();
    render(<ImagePreviewProvider><PreviewImage src="/one.png" /><PreviewImage src="/two.png" alt="Two" /></ImagePreviewProvider>);
    await user.click(screen.getByRole('button', { name: 'Expand Image' }));
    expect(screen.getByRole('dialog', { name: 'Image' })).toBeInTheDocument();
    await user.keyboard('{ArrowRight}');
    expect(screen.getByRole('dialog', { name: 'Two' })).toBeInTheDocument();
  });

  it('uses page order when earlier images mount later and removes stale entries', async () => {
    const user = userEvent.setup();
    function Images({ earlier = false, later = true }) {
      return <ImagePreviewProvider>
        {earlier && <PreviewImage src="/earlier.png" alt="Earlier" />}
        {later && <PreviewImage src="/later.png" alt="Later" />}
        <PreviewImage alt="Missing source" />
      </ImagePreviewProvider>;
    }
    const view = render(<Images />);
    view.rerender(<Images earlier />);
    await user.click(screen.getByRole('button', { name: 'Expand Later' }));
    expect(screen.getByText('2 / 2')).toBeInTheDocument();
    await user.keyboard('{ArrowLeft}');
    expect(screen.getByRole('dialog', { name: 'Earlier' })).toBeInTheDocument();
    view.rerender(<Images earlier={false} later />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Expand Missing source' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('does not intercept arrow keys in another focused dialog', async () => {
    const user = userEvent.setup();
    render(<Gallery />);
    await user.click(screen.getByRole('button', { name: 'Expand First' }));
    const other = document.createElement('div');
    other.setAttribute('role', 'dialog');
    other.tabIndex = -1;
    document.body.append(other);
    other.focus();
    await user.keyboard('{ArrowRight}');
    expect(screen.getByRole('dialog', { name: 'First' })).toBeInTheDocument();
    other.remove();
  });

  it('keeps duplicate occurrences stable when older history is prepended and after reload', async () => {
    const user = userEvent.setup();
    function History({ older = false, initial = '/session/example' }) {
      return <MemoryRouter initialEntries={[initial]}><LocationControls /><ImagePreviewProvider>
        {older && <div data-message-id="older-message"><PreviewImage src="/same.png" alt="Older copy" /></div>}
        <div data-message-id="newer-message"><PreviewImage src="/same.png" alt="Newer copy" /></div>
      </ImagePreviewProvider></MemoryRouter>;
    }
    const view = render(<History />);
    await user.click(screen.getByRole('button', { name: 'Expand Newer copy' }));
    const newerURL = screen.getByTestId('url').textContent!;
    view.rerender(<History older />);
    expect(screen.getByRole('dialog', { name: 'Newer copy' })).toBeInTheDocument();
    expect(screen.getByText('2 / 2')).toBeInTheDocument();
    expect(screen.getByTestId('url').textContent).toBe(newerURL);
    await user.keyboard('{ArrowLeft}');
    const olderURL = screen.getByTestId('url').textContent!;
    view.unmount();
    const reloaded = render(<History initial={olderURL} />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    reloaded.rerender(<History older initial={olderURL} />);
    expect(screen.getByRole('dialog', { name: 'Older copy' })).toBeInTheDocument();
    await user.keyboard('{ArrowRight}');
    expect(screen.getByRole('dialog', { name: 'Newer copy' })).toBeInTheDocument();
    expect(screen.getByTestId('url').textContent).toBe(newerURL);
  });
});
