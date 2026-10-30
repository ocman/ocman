import { createContext } from 'react';

/** Public shares have no authenticated image-wrapper endpoint. */
export const ExternalImageConsentContext = createContext(true);
