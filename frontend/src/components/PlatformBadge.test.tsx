// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useMultiPlatform } from '../lib/useCapabilities';
import { PlatformBadge, PlatformLogo } from './PlatformBadge';

vi.mock('../lib/useCapabilities', () => ({ useMultiPlatform: vi.fn() }));

beforeEach(() => vi.mocked(useMultiPlatform).mockReturnValue(true));

it('renders decorative platform logos, including remote platforms, without broken unknown icons', () => {
	const { container, rerender } = render(<PlatformLogo platform="r-owner:claudecode" />);
	expect(container.querySelector('img')).toHaveAttribute('src', '/platform-claudecode.svg');
	expect(container.querySelector('img')).toHaveAttribute('alt', '');
	rerender(<PlatformLogo platform="custom" />);
	expect(container).toBeEmptyDOMElement();
});

it('hides redundant badges when only one platform is available', () => {
	vi.mocked(useMultiPlatform).mockReturnValue(false);
	const { container } = render(<PlatformBadge platform="opencode" />);
	expect(container).toBeEmptyDOMElement();
});

it('renders known, remote, and unknown platform labels', () => {
	const { rerender } = render(<PlatformBadge platform="opencode" />);
	expect(screen.getByLabelText('OpenCode')).toHaveTextContent('OpenCode');

	rerender(<PlatformBadge platform="r-owner:opencode" variant="compact" />);
	expect(screen.getByLabelText('OpenCode')).toHaveTextContent('OC');
	expect(screen.getByLabelText('OpenCode')).toHaveClass('platform-opencode', 'compact');

	rerender(<PlatformBadge platform="claudecode" />);
	expect(screen.getByLabelText('Claude Code')).toHaveTextContent('Claude Code');

	rerender(<PlatformBadge platform="custom" variant="plain" />);
	expect(screen.getByLabelText('custom')).toHaveTextContent('CU');
	expect(screen.getByLabelText('custom')).toHaveClass('platform-custom', 'plain');
});

it('shows accessible icon-only indicators with a platform explanation', async () => {
	const { rerender } = render(<PlatformBadge platform="opencode" />);
	expect(screen.getByLabelText('OpenCode').querySelector('img')).toBeNull();
	for (const [platform, label, tip, remote] of [
		['r-owner:opencode', 'OpenCode (remote machine)', 'This session runs on OpenCode on a remote machine.', true],
		['claudecode', 'Claude Code', 'This session runs on Claude Code.', false],
	] as const) {
		rerender(<PlatformBadge platform={platform} variant="icon" />);
		const icon = screen.getByLabelText(label).querySelector('img');
		expect(icon).toHaveAttribute('src', `/platform-${platform.split(':').pop()}.svg`);
		expect(icon).toHaveAttribute('alt', '');
		expect(icon).toHaveAttribute('width', '16');
		expect(icon).toHaveAttribute('height', '16');
		expect(screen.queryByTestId('platform-remote-icon') !== null).toBe(remote);
		expect(screen.getByLabelText(label)).toHaveAttribute('tabindex', '0');
		fireEvent.focus(screen.getByLabelText(label));
		expect(await screen.findByRole('tooltip')).toHaveTextContent(tip);
		fireEvent.blur(screen.getByLabelText(label));
	}
	rerender(<PlatformBadge platform="custom" variant="icon" />);
	expect(screen.getByLabelText('custom').querySelector('img')).toBeNull();
});
