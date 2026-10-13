import { createContext } from 'react';

/** Platform PR/Issue launches start on: the active session's (FR-2). */
export const LaunchPlatformContext = createContext<string | undefined>(undefined);
