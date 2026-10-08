import { describe, expect, it } from 'vitest';
import { SETTINGS, searchSettings, type SettingId } from './settingsCatalog';

const sources = import.meta.glob(['../**/*.tsx', '!../**/*.test.tsx'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>;
const labels = { sessions: 'Sessions', 'auto-approve': 'Auto-approve', sharing: 'Sharing', notifications: 'Notifications' };

describe('settings catalog', () => {
  it.each(Object.entries(SETTINGS))('%s has a title, description and example', (_id, entry) => {
    expect(entry.title.trim()).not.toBe('');
    expect(entry.description.trim().length).toBeGreaterThan(20);
    expect(entry.example.trim().length).toBeGreaterThan(10);
  });

  it('only lists settings a row actually renders', () => {
    const all = Object.values(sources).join('\n');
    const unused = (Object.keys(SETTINGS) as SettingId[]).filter((id) => !all.includes(`setting="${id}"`));
    expect(unused).toEqual([]);
  });
});

describe('searchSettings', () => {
  const ids = (q: string) => searchSettings(q, labels).map((r) => r.id);

  it('matches title, description, example, keywords and group, all words required', () => {
    expect(ids('bell')).toEqual(['bell-sound']);
    expect(ids('quota reset')).toContain('model-fallthrough-cooldown');
    expect(ids('haiku')).toEqual(['reviewer-model']);
    expect(ids('tts')).toContain('read-aloud');
    expect(ids('auto-approve')).toEqual(expect.arrayContaining(['reviewer-model', 'human-review-window']));
    expect(ids('bell zzz')).toEqual([]);
    expect(ids('   ')).toEqual([]);
  });

  it('ranks title matches first and skips groups not shown', () => {
    expect(ids('reviewer')[0]).toMatch(/^reviewer-/);
    expect(ids('sign out')).toEqual([]); // Account group not in labels
    expect(ids('models')).not.toContain('project-add-model');
  });
});
