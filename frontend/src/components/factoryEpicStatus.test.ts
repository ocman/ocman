import { describe, expect, it } from 'vitest';
import type { FactoryEpic } from '../lib/api';
import { factoryEpicStatus } from './factoryEpicStatus';

describe('Factory delivery status', () => {
  it.each([
    ['pending', false, 'Implementation complete, delivery pending', 'idle'],
    ['pending', true, 'Implementation complete, delivery blocked', 'action'],
    ['ready_for_review', false, 'Ready for review', 'done'],
  ] as const)('shows %s with stuck=%s', (deliveryStatus, stuck, text, tone) => {
    const epic = { status: 'open', progress: { requiredTotal: 3, requiredSucceeded: 2, optionalOpen: 0, deliveryStatus, stuck } } as FactoryEpic;
    expect(factoryEpicStatus(epic)).toEqual({ text, tone });
  });
});
