import { useCallback, useRef } from 'react';

export function usePaletteFocus() {
  const inputRef = useRef<HTMLInputElement>(null);
  const origin = useRef<Element | null>(null);

  const focusInput = useCallback((input: HTMLInputElement | null) => {
    inputRef.current = input;
    if (!input || document.activeElement === input) return;
    origin.current = document.activeElement;
    input.focus();
  }, []);

  const restoreFocus = useCallback(() => {
    const target = origin.current;
    if (target instanceof HTMLElement && target.isConnected) target.focus({ preventScroll: true });
  }, []);

  return { inputRef, focusInput, restoreFocus };
}
