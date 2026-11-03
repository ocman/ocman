import { useAuiState, type MessageState } from '@assistant-ui/react';

// assistant-ui 0.15 dropped `useMessage`; same selector shape over the aui store.
export const useMessage = <T,>(selector: (m: MessageState) => T): T => useAuiState((s) => selector(s.message));
