import '@testing-library/jest-dom/vitest';
import { configure } from '@testing-library/react';

// Cold integration renders can take several seconds on shared CI runners.
// Per-call waitFor timeouts still override this default.
configure({ asyncUtilTimeout: 5000 });

export { cleanup } from '@testing-library/react';
