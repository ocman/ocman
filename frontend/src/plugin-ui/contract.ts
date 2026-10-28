// Compile-time check that the host implements the published contract.
import type * as Contract from '@ocman/plugin-ui';
import * as Implementation from './index';

export const contract: typeof Contract = Implementation;
