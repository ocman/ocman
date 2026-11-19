// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import * as PluginUI from './index';

it('renders plugin Markdown using host prose without executing raw HTML or unsafe links', () => {
  const { container } = render(<PluginUI.Markdown text={'## Decisions\n\n**Ship it**\n\n- First\n- Second\n\n<script>alert(1)</script>\n\n[unsafe](javascript:alert(1))'} />);
  expect(screen.getByRole('heading', { name: 'Decisions' })).toBeInTheDocument();
  expect(screen.getByText('Ship it').tagName).toBe('STRONG');
  expect(screen.getAllByRole('listitem')).toHaveLength(2);
  expect(container.querySelector('.oc-md')).not.toBeNull();
  expect(container.querySelector('script')).toBeNull();
  expect(container.querySelector('a[href^="javascript:"]')).toBeNull();
});
