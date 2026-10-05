// @vitest-environment jsdom
//
// The "no session open" hint must name the shortcut that actually opens
// the command palette (registered by GlobalHotkeys), not ⌘K.

import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/react';
import { renderSessionPage } from './harness';
import { useShortcutRegistry } from '../../../lib/shortcutRegistry';

describe('SessionDetail — empty state', () => {
  it('names the registered command-palette shortcut', async () => {
    useShortcutRegistry.getState().register({
      id: 'site.command-palette',
      scope: 'site',
      keys: { code: 'Space', alt: true },
      label: 'Alt+Space',
      description: 'Open command palette',
      handler: () => {},
    });
    renderSessionPage({ sessionId: 'new' });
    const empty = await screen.findByTestId('empty-detail');
    expect(empty.querySelector('kbd')?.textContent).toBe('Alt+Space');
  });
});
