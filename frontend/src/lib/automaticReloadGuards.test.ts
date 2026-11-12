import { expect, it } from 'vitest';
import { hasAutomaticReloadGuards, registerAutomaticReloadGuard } from './automaticReloadGuards';

it('reads current owner state synchronously and unregisters only that owner', () => {
  let blocked = false;
  const first = registerAutomaticReloadGuard(() => blocked);
  const second = registerAutomaticReloadGuard(() => true);
  expect(hasAutomaticReloadGuards()).toBe(true);
  second();
  expect(hasAutomaticReloadGuards()).toBe(false);
  blocked = true;
  expect(hasAutomaticReloadGuards()).toBe(true);
  first(); first();
  expect(hasAutomaticReloadGuards()).toBe(false);
});

it('fails closed when a live owner cannot be read', () => {
  const dispose = registerAutomaticReloadGuard(() => { throw new Error('unavailable'); });
  try { expect(hasAutomaticReloadGuards()).toBe(true); }
  finally { dispose(); }
  expect(hasAutomaticReloadGuards()).toBe(false);
});
