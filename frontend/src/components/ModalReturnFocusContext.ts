import { createContext, type RefObject } from 'react';

// A dialog's owner can return focus to an editor instead of its trigger button.
export const ModalReturnFocusContext = createContext<RefObject<HTMLElement | null> | null>(null);
