import { createContext, type ReactNode } from 'react';

export const ConversationLaunchContext = createContext<{ messageId: string; setup: ReactNode; timestamp?: number } | null>(null);
