// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { PreviewImage } from './ImagePreview';
import { ExternalImageConsentContext } from '../lib/externalImageContext';

describe('external image consent', () => {
  it('never offers the authenticated opt-in endpoint in public shared views', () => {
    const { container } = render(<ExternalImageConsentContext.Provider value={false}><PreviewImage src="https://images.example/report.png" alt="Shared" /></ExternalImageConsentContext.Provider>);
    expect(screen.getByText('External images are blocked in shared views.')).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(container.querySelector('img, iframe')).toBeNull();
  });
  it('creates no image or iframe until the user opts in, then uses an opaque sandbox', () => {
    const src = 'https://images.example/report.svg?private=value';
    const { container } = render(<PreviewImage src={src} alt="Report" />);
    expect(container.querySelector('img, iframe')).toBeNull();
    expect(screen.getByText(/Loading contacts images.example/)).toBeInTheDocument();
    expect(screen.getByText(src)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Load external image from images.example' }));
    const frame = screen.getByTitle('External image: Report');
    expect(frame).toHaveAttribute('sandbox', '');
    expect(frame).toHaveAttribute('referrerpolicy', 'no-referrer');
    expect(frame).toHaveAttribute('src', `/api/previews/external-image?url=${encodeURIComponent(src)}`);
    fireEvent.click(screen.getByRole('button', { name: 'Stop loading external image' }));
    expect(container.querySelector('img, iframe')).toBeNull();
  });

  it('never carries consent to a different image or restores it after remount', () => {
    const { rerender, container } = render(<PreviewImage src="https://one.example/image.png" alt="One" />);
    fireEvent.click(screen.getByRole('button', { name: 'Load external image from one.example' }));
    expect(container.querySelector('iframe')).not.toBeNull();
    rerender(<PreviewImage src="https://two.example/image.png" alt="Two" />);
    expect(container.querySelector('iframe')).toBeNull();
    rerender(<PreviewImage src="https://one.example/image.png" alt="One" />);
    expect(container.querySelector('iframe')).toBeNull();
  });

  it.each(['javascript:alert(1)', 'file:///etc/passwd', 'ftp://example.com/x', 'https://user:password@example.com/image'])('refuses unsafe image source %s', src => {
    const { container } = render(<PreviewImage src={src} alt="Unsafe" />);
    expect(container.querySelector('img, iframe')).toBeNull();
    expect(screen.getByText('Image source is not allowed.')).toBeInTheDocument();
  });

  it('keeps local and embedded images in the native gallery', () => {
    render(<PreviewImage src="/local.png" alt="Local" />);
    expect(screen.getByRole('img', { name: 'Local' })).toHaveAttribute('src', '/local.png');
    expect(screen.getByRole('button', { name: 'Expand Local' })).toBeInTheDocument();
  });
});
