// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { observeMessageLinks } from './linkHardener';

afterEach(() => { document.body.replaceChildren(); vi.useRealTimers(); vi.unstubAllGlobals(); });

it('hardens initial and added links without rescanning history or outside-message nodes', async () => {
  vi.useFakeTimers();
  const thread = document.createElement('div');
  thread.innerHTML = '<div class="oc-msg-body"><a href="/old">old</a></div><aside></aside>';
  document.body.append(thread);
  const stop = observeMessageLinks(thread);
  expect(thread.querySelector('a')?.rel).toBe('noopener noreferrer');
  const scan = vi.spyOn(thread, 'querySelectorAll');
  const body = thread.firstElementChild!;
  const direct = document.createElement('a'); direct.href = '/direct'; body.append(direct);
  const wrapper = document.createElement('span'); wrapper.innerHTML = '<b><a href="/nested">nested</a></b>';
  body.append(wrapper);
  const fragment = document.createDocumentFragment();
  const message = document.createElement('div'); message.className = 'oc-msg-body'; message.innerHTML = '<a href="/fragment">fragment</a>';
  fragment.append(message); thread.append(fragment);
  const outside = document.createElement('a'); outside.href = '/outside'; thread.querySelector('aside')!.append(outside);
  await Promise.resolve();
  await vi.advanceTimersByTimeAsync(200);
  expect(scan).not.toHaveBeenCalled();
  for (const a of thread.querySelectorAll<HTMLAnchorElement>('.oc-msg-body a')) {
    expect(a.target).toBe('_blank'); expect(a.rel).toBe('noopener noreferrer');
  }
  expect(outside.target).toBe('');
  stop();
});

it('coalesces many mutations, skips removals and detached additions, and cancels disposal work', async () => {
  vi.useFakeTimers();
  const thread = document.createElement('div'); thread.className = 'oc-msg-body'; document.body.append(thread);
  const stop = observeMessageLinks(thread);
  const wrapper = document.createElement('span'); thread.append(wrapper);
  for (let i = 0; i < 100; i++) { const a = document.createElement('a'); a.href = '/'; wrapper.append(a); }
  const detached = document.createElement('a'); detached.href = '/'; thread.append(detached); detached.remove();
  const scan = vi.spyOn(wrapper, 'querySelectorAll');
  await Promise.resolve();
  await vi.advanceTimersByTimeAsync(200);
  expect(scan).toHaveBeenCalledTimes(1);
  expect(detached.target).toBe('');
  const late = document.createElement('a'); late.href = '/'; thread.append(late);
  await Promise.resolve(); stop();
  await vi.advanceTimersByTimeAsync(500);
  expect(late.target).toBe('');
  thread.append(document.createElement('a'));
});

it('uses and cancels idle scheduling when available', async () => {
  let callback!: IdleRequestCallback;
  vi.stubGlobal('requestIdleCallback', vi.fn((cb) => { callback = cb; return 42; }));
  vi.stubGlobal('cancelIdleCallback', vi.fn());
  const thread = document.createElement('div'); thread.className = 'oc-msg-body'; document.body.append(thread);
  const stop = observeMessageLinks(thread);
  thread.innerHTML = '<a href="/">link</a>';
  await Promise.resolve();
  callback({ didTimeout: false, timeRemaining: () => 50 });
  expect(thread.querySelector('a')?.target).toBe('_blank');
  thread.append(document.createElement('span'));
  await Promise.resolve(); stop();
  expect(cancelIdleCallback).toHaveBeenCalledWith(42);
});

it('ignores text-only and removal-only mutations and subtrees moved outside the thread', async () => {
  vi.useFakeTimers();
  const thread = document.createElement('div'); thread.className = 'oc-msg-body';
  thread.innerHTML = '<span>old text</span>'; document.body.append(thread);
  const stop = observeMessageLinks(thread);
  thread.firstChild!.remove(); thread.append('streamed text');
  await Promise.resolve();
  expect(vi.getTimerCount()).toBe(0);
  const wrapper = document.createElement('span'); wrapper.innerHTML = '<a href="/">moved</a>';
  thread.append(wrapper); await Promise.resolve(); document.body.append(wrapper);
  await vi.advanceTimersByTimeAsync(200);
  expect(wrapper.querySelector('a')?.target).toBe('');
  thread.innerHTML = '<a href="/">detached thread</a>';
  await Promise.resolve(); thread.remove();
  await vi.advanceTimersByTimeAsync(200);
  expect(thread.querySelector('a')?.target).toBe('');
  stop();
});
