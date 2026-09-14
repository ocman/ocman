// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useMultiPlatform } from '../lib/useCapabilities';
import { PlatformBadge } from './PlatformBadge';

vi.mock('../lib/useCapabilities', () => ({ useMultiPlatform: vi.fn() }));

beforeEach(() => vi.mocked(useMultiPlatform).mockReturnValue(true));

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

	rerender(<PlatformBadge platform="custom" variant="plain" />);
	expect(screen.getByLabelText('custom')).toHaveTextContent('CU');
	expect(screen.getByLabelText('custom')).toHaveClass('platform-custom', 'plain');
});
