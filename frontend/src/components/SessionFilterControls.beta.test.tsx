// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { SessionFilterControls } from './SessionFilterControls';
import { useBetaFeatures } from '../lib/betaFeatures';

afterEach(cleanup);

it.each([false, true])('only offers the Factory filter when enabled=%s', (factory) => {
  useBetaFeatures.setState({ factory });
  render(<SessionFilterControls value={{ alwaysShowPrompts: true, showArchived: false, showSnoozed: false, showChildren: false, showFactory: false, showRoutines: false, showPlugins: false }} onChange={vi.fn()} />);
  expect(!!screen.queryByRole('checkbox', { name: 'Show factory' })).toBe(factory);
  expect(screen.getByRole('checkbox', { name: 'Show routines' })).toBeInTheDocument();
});
