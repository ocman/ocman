import { describe, expect, it } from 'vitest';
import { SETTINGS_CATEGORIES, settingsCategory } from './settingsNavigation';
import { SETTINGS } from './settingsCatalog';

describe('settings categories', () => {
  it.each(SETTINGS_CATEGORIES)('opens the $label category directly', ({ id }) => {
    expect(settingsCategory(id)).toBe(id);
  });

  it.each([null, '', 'unknown', 'constructor', 'toString'])('defaults to app for %s', (tab) => {
    expect(settingsCategory(tab)).toBe('app');
  });

  it('assigns every global search result to a visible category', () => {
    const categories = SETTINGS_CATEGORIES.map(({ id }) => id);
    for (const entry of Object.values(SETTINGS)) {
      if (entry.group !== 'project') expect(categories).toContain(entry.group);
    }
  });
});
