// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';
import { useApiStore } from './apiStore';
import { getSessionModel, saveSessionModel } from './sessionModel';

describe('session model choices', () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => vi.restoreAllMocks());

  it('persists the requested model when sending, even if delivery fails', async () => {
    vi.spyOn(api, 'sendMessage').mockRejectedValue(new Error('offline'));
    await expect(useApiStore.getState().sendMessage('s1', 'hello', undefined, 'prov/chosen')).rejects.toThrow('offline');
    expect(getSessionModel('s1')).toBe('prov/chosen');
  });

  it('does not erase a selection when sending without a model', async () => {
    saveSessionModel('s1', 'prov/chosen');
    vi.spyOn(api, 'sendMessage').mockResolvedValue(undefined);
    await useApiStore.getState().sendMessage('s1', 'hello');
    expect(getSessionModel('s1')).toBe('prov/chosen');
  });

  it('keeps choices separate and ignores empty writes', () => {
    saveSessionModel('s1', 'prov/first');
    saveSessionModel('s2', 'prov/second');
    saveSessionModel('', 'prov/ignored');
    saveSessionModel('s1', '');
    expect(getSessionModel('s1')).toBe('prov/first');
    expect(getSessionModel('s2')).toBe('prov/second');
    expect(getSessionModel('')).toBe('');
  });

  it.each(['broken', 'null', '[]', '42', '{"s1":7,"s2":"prov/valid"}'])('tolerates invalid stored choices: %s', (raw) => {
    localStorage.setItem('ocman.sessionModels.v1', raw);
    expect(getSessionModel('s1')).toBe('');
    saveSessionModel('s3', 'prov/new');
    expect(getSessionModel('s3')).toBe('prov/new');
    if (raw.includes('s2')) expect(getSessionModel('s2')).toBe('prov/valid');
  });

  it('does not block model selection when storage is unavailable', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked'); });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('quota'); });
    expect(() => saveSessionModel('s1', 'prov/chosen')).not.toThrow();
    expect(getSessionModel('s1')).toBe('');
  });
});
