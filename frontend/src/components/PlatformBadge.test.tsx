// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useCapabilities, useMultiPlatform } from '../lib/useCapabilities';
import { PlatformBadge, PlatformLogo } from './PlatformBadge';
// @ts-expect-error Vitest runs in Node; application types intentionally exclude Node globals.
import { readFileSync } from 'node:fs';

vi.mock('../lib/useCapabilities', () => ({ useMultiPlatform: vi.fn(), useCapabilities: vi.fn() }));

beforeEach(() => {
	vi.mocked(useMultiPlatform).mockReturnValue(true);
	vi.mocked(useCapabilities).mockReturnValue(null);
});

it('keeps platform icon images at zero vertical offset', () => {
	const style = document.createElement('style');
	style.textContent = readFileSync('src/components/PlatformBadge.css', 'utf8');
	document.head.appendChild(style);
	try {
		const { container } = render(<PlatformBadge platform="opencode" variant="icon" />);
		expect(getComputedStyle(container.querySelector('img')!).top).toBe('0px');
	} finally {
		style.remove();
	}
});

it('applies a platform accent to each known platform pill', () => {
	const style = document.createElement('style');
	style.textContent = readFileSync('src/components/PlatformBadge.css', 'utf8');
	document.head.appendChild(style);
	try {
		const rules = Array.from(style.sheet!.cssRules) as CSSStyleRule[];
		for (const [platform, label] of [['opencode', 'OpenCode'], ['claudecode', 'Claude Code']]) {
			const { unmount } = render(<PlatformBadge platform={platform} />);
			const badge = screen.getByLabelText(label);
			const accent = rules.find((rule) => rule.selectorText?.includes(`.platform-${platform}`) && badge.matches(rule.selectorText));
			expect(accent?.style.getPropertyValue('color') || '', platform).toContain('var(');
			unmount();
		}
	} finally {
		style.remove();
	}
});

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

	rerender(<PlatformBadge platform="codex" variant="compact" />);
	expect(screen.getByLabelText('Codex')).toHaveTextContent('CX');

	rerender(<PlatformBadge platform="custom" variant="plain" />);
	expect(screen.getByLabelText('custom')).toHaveTextContent('CU');
	expect(screen.getByLabelText('custom')).toHaveClass('platform-custom', 'plain');
});

it('shows accessible icon-only indicators with a platform explanation', async () => {
	const { rerender } = render(<PlatformBadge platform="opencode" />);
	expect(screen.getByLabelText('OpenCode').querySelector('img')).toBeNull();
	for (const [platform, label, tip] of [
		['r-owner:opencode', 'OpenCode (remote machine)', 'This session runs on OpenCode on a remote machine.'],
		['claudecode', 'Claude Code', 'This session runs on Claude Code.'],
		['r-owner:codex', 'Codex (remote machine)', 'This session runs on Codex on a remote machine.'],
	] as const) {
		rerender(<PlatformBadge platform={platform} variant="icon" />);
		const icon = screen.getByLabelText(label).querySelector('img');
		expect(icon).toHaveAttribute('src', `/platform-${platform.split(':').pop()}.svg`);
		expect(icon).toHaveAttribute('alt', '');
		expect(icon).toHaveAttribute('width', '16');
		expect(icon).toHaveAttribute('height', '16');
		expect(screen.queryByTestId('platform-remote-icon')).not.toBeInTheDocument();
		expect(screen.getByLabelText(label)).toHaveAttribute('tabindex', '0');
		fireEvent.focus(screen.getByLabelText(label));
		expect(await screen.findByRole('tooltip')).toHaveTextContent(tip);
		fireEvent.blur(screen.getByLabelText(label));
	}
	rerender(<PlatformBadge platform="custom" variant="icon" />);
	expect(screen.getByLabelText('custom').querySelector('img')).toBeNull();
});

it.each(['cnx_work', 'r-owner:cnx_work'])('uses the named platform label and native icon for %s', async (platform) => {
	vi.mocked(useCapabilities).mockReturnValue({ platforms: [{ id: platform, kind: 'claudecode', displayName: 'Claude Work' }] } as ReturnType<typeof useCapabilities>);
	const label = platform.startsWith('r-') ? 'Claude Work (remote machine)' : 'Claude Work';
	const { container, rerender } = render(<PlatformBadge platform={platform} variant="icon" alwaysShow />);
	expect(screen.getByLabelText(label).querySelector('img')).toHaveAttribute('src', '/platform-claudecode.svg');
	fireEvent.focus(screen.getByLabelText(label));
	expect(await screen.findByRole('tooltip')).toHaveTextContent('Claude Work');
	expect(screen.getByRole('tooltip')).not.toHaveTextContent('cnx_');
	fireEvent.blur(screen.getByLabelText(label));
	rerender(<PlatformBadge platform={platform} />);
	expect(screen.getByLabelText('Claude Work')).toHaveTextContent('Claude Work');
	expect(screen.getByLabelText('Claude Work')).toHaveClass('platform-claudecode');
	rerender(<PlatformLogo platform={platform} />);
	expect(container.querySelector('img')).toHaveAttribute('src', '/platform-claudecode.svg');
});

it('never shows an internal connection key while metadata is unavailable', async () => {
	render(<PlatformBadge platform="cnx_missing" variant="icon" alwaysShow />);
	const badge = screen.getByLabelText('Platform');
	fireEvent.focus(badge);
	expect(await screen.findByRole('tooltip')).not.toHaveTextContent('cnx_missing');
});

it('does not borrow a local account name for a remote connection and updates when its metadata arrives', () => {
	vi.mocked(useCapabilities).mockReturnValue({ platforms: [
		{ id: 'cnx_work', kind: 'codex', displayName: 'Local work' },
	] } as ReturnType<typeof useCapabilities>);
	const { container, rerender } = render(<PlatformLogo platform="r-owner:cnx_work" />);
	expect(container).toBeEmptyDOMElement();
	rerender(<PlatformBadge platform="r-owner:cnx_work" variant="icon" />);
	expect(screen.getByLabelText('Platform (remote machine)')).toBeVisible();
	expect(screen.queryByLabelText('Local work (remote machine)')).not.toBeInTheDocument();
	vi.mocked(useCapabilities).mockReturnValue({ platforms: [
		{ id: 'r-owner:cnx_work', kind: 'codex', displayName: 'Remote work' },
	] } as ReturnType<typeof useCapabilities>);
	rerender(<PlatformBadge platform="r-owner:cnx_work" variant="compact" />);
	expect(screen.getByLabelText('Remote work')).toHaveTextContent('CX');
});
