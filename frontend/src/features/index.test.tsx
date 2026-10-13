import { matchRoutes } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { FEATURES, featureNav, featureRoutes, featureSettings, featureTitle } from '.';
import { SETTINGS } from '../lib/settingsCatalog';

const routes = FEATURES.flatMap((f) => f.routes ?? []);

describe('feature registry', () => {
  it('gives every feature and route a unique key', () => {
    expect(new Set(FEATURES.map((f) => f.id)).size).toBe(FEATURES.length);
    expect(new Set(routes.map((r) => r.path)).size).toBe(routes.length);
    expect(new Set(featureSettings.map((s) => s.id)).size).toBe(featureSettings.length);
  });

  it.each([
    ['/routines', 'dashboard'],
    ['/artifacts', 'dashboard'],
    ['/artifacts/abc', 'dashboard'],
    ['/factory', 'page'],
    ['/factory/overview', 'page'],
    ['/factory/epics/e1', 'page'],
    ['/factory/issues', 'page'],
    ['/factory/issues/i1', 'page'],
    ['/factory/configuration', 'page'],
  ])('keeps %s in the %s layout', (url, layout) => {
    const match = matchRoutes(featureRoutes(layout as 'dashboard' | 'page'), url);
    expect(match?.[0].route.path).toBeDefined();
  });

  it('only links nav entries to registered routes', () => {
    for (const item of featureNav) {
      expect(matchRoutes(routes, item.to), item.to).not.toBeNull();
    }
  });

  it('only registers settings groups the search catalog can reveal', () => {
    const groups = new Set(Object.values(SETTINGS).map((s) => s.group));
    for (const section of featureSettings) expect(groups, section.id).toContain(section.id);
  });

  it.each([
    ['/factory/how-to', 'Factory how to'],
    ['/factory/queue', 'Factory queue'],
    ['/factory/epics/e1', 'Factory'],
    ['/routines', 'Routines'],
    ['/artifacts/abc', 'Artifacts'],
    ['/factory', undefined],
    ['/sessions', undefined],
  ])('titles %s as %s', (path, title) => {
    expect(featureTitle(path)).toBe(title);
  });
});
