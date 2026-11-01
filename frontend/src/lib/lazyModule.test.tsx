// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { lazyModule, useLazyModule } from './lazyModule';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (err: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

describe('lazyModule', () => {
  it('loads once on demand and shares the result', async () => {
    const importer = vi.fn(async () => 'loaded');
    const module = lazyModule(importer);
    expect(module.get()).toBeUndefined();
    expect(importer).not.toHaveBeenCalled();
    await Promise.all([module.load(), module.load()]);
    expect(importer).toHaveBeenCalledOnce();
    expect(module.get()).toBe('loaded');
  });

  it('retries after a failed load', async () => {
    const importer = vi.fn()
      .mockRejectedValueOnce(new Error('chunk failed'))
      .mockResolvedValueOnce('loaded');
    const module = lazyModule<string>(importer);
    await expect(module.load()).rejects.toThrow('chunk failed');
    expect(module.get()).toBeUndefined();
    await expect(module.load()).resolves.toBe('loaded');
    expect(importer).toHaveBeenCalledTimes(2);
  });

  it('renders the fallback until the module loads, then re-renders', async () => {
    const pending = deferred<string>();
    const module = lazyModule(() => pending.promise);
    function Probe() {
      return <p>{useLazyModule(module) ?? 'fallback'}</p>;
    }
    render(<Probe />);
    expect(screen.getByText('fallback')).toBeInTheDocument();
    pending.resolve('ready');
    expect(await screen.findByText('ready')).toBeInTheDocument();
  });

  it('keeps the fallback when the load fails', async () => {
    const pending = deferred<string>();
    const module = lazyModule(() => pending.promise);
    function Probe() {
      return <p>{useLazyModule(module) ?? 'fallback'}</p>;
    }
    render(<Probe />);
    pending.reject(new Error('offline'));
    await expect(module.load()).rejects.toThrow('offline');
    expect(screen.getByText('fallback')).toBeInTheDocument();
  });
});
