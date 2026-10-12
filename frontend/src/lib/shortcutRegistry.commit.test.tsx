// @vitest-environment jsdom
import { useLayoutEffect } from 'react';
import { act, render } from '@testing-library/react';
import { expect, it } from 'vitest';
import { useShortcut, useShortcutDispatcher } from './shortcutRegistry';

// A key pressed right after a re-render must reach the handler for the
// committed state, not the previous render's closure (Inbox Delete once
// re-archived the message it had just archived).
it('updates the handler in the same commit as the DOM', () => {
  const handled: number[] = [];
  function Item({ n }: { n: number }) {
    useShortcut({ id: 'test.commit', scope: 'site', keys: [{ code: 'Delete' }], description: 'test', handler: () => { handled.push(n); } });
    return null;
  }
  // Fires during the commit, before passive effects would have run.
  function PressAfterCommit({ n }: { n: number }) {
    useLayoutEffect(() => {
      if (n === 2) window.dispatchEvent(new KeyboardEvent('keydown', { code: 'Delete', key: 'Delete' }));
    }, [n]);
    return null;
  }
  function App({ n }: { n: number }) {
    useShortcutDispatcher();
    return <><Item n={n} /><PressAfterCommit n={n} /></>;
  }
  const view = render(<App n={1} />);
  act(() => view.rerender(<App n={2} />));
  expect(handled).toEqual([2]);
});
