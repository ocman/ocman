// @vitest-environment jsdom
import { beforeEach, expect, it } from 'vitest';
import { useBetaFeatures } from './betaFeatures';

beforeEach(() => {
  useBetaFeatures.setState({ factory: false });
  localStorage.clear();
});

it('starts with Factory disabled for new and existing browsers without an opt-in', async () => {
  expect(useBetaFeatures.getInitialState().factory).toBe(false);
  await useBetaFeatures.persist.rehydrate();
  expect(useBetaFeatures.getState().factory).toBe(false);
});

it.each([true, false])('persists Factory opt-in %s across hydration', async (enabled) => {
  useBetaFeatures.getState().setFactory(enabled);
  const stored = localStorage.getItem('ocman-beta-features');
  expect(JSON.parse(stored ?? '{}').state).toEqual({ factory: enabled });
  useBetaFeatures.setState({ factory: !enabled });
  localStorage.setItem('ocman-beta-features', stored!);
  await useBetaFeatures.persist.rehydrate();
  expect(useBetaFeatures.getState().factory).toBe(enabled);
});
