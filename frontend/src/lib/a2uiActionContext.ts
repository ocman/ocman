import { createContext } from 'react';
import type { A2UIAction } from './a2ui';

/** Missing callback means disconnected, shared, or otherwise read-only. */
export const A2UIActionContext = createContext<{ scope: string; send?: (action: A2UIAction) => Promise<void> }>({ scope: 'read-only' });
