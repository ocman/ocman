// @vitest-environment jsdom
import { screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { renderSessionPage, makeSession, makeSessionDetail } from './harness';

vi.mock('../../../components/FactoryPlanApproval', () => ({ FactoryPlanApproval: () => <aside aria-label="Factory plan approval" /> }));
vi.mock('../../../components/FactorySessionRecovery', () => ({ FactorySessionRecovery: () => <aside aria-label="Factory recovery" /> }));

beforeEach(() => { HTMLElement.prototype.scrollTo = vi.fn(); });

it('places Factory panels at the bottom of the scrollable conversation, outside the composer', async () => {
  renderSessionPage({ sessionId: 'sess_1', realAssistantThread: true, detail: makeSessionDetail(makeSession({ factoryAttemptId: 'attempt' })) });
  const recovery = await screen.findByLabelText('Factory recovery');
  const approval = screen.getByLabelText('Factory plan approval');
  const viewport = screen.getByTestId('conversation-viewport');
  const composer = screen.getByTestId('conversation-composer');
  for (const panel of [approval, recovery]) {
    expect(viewport.lastElementChild).toContainElement(panel);
    expect(composer).not.toContainElement(panel);
  }
});
