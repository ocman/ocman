// @vitest-environment jsdom
import { act, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { flushPromises, renderSessionPage } from './harness';
import { useUiStore } from '../../../lib/uiStore';

vi.mock('../NewConversation', () => ({ NewConversation: () => <div>New draft</div> }));
vi.mock('../../../components/RightPanel', () => ({
  RightPanel: (props: { directory?: string; remoteId?: string; platformId?: string; session?: unknown }) =>
    <output data-testid="project-context">{JSON.stringify(props)}</output>,
}));

it.each([
  { remoteId: 'local', platform: 'opencode', key: '/other' },
  { remoteId: 'box', platform: 'r-box:opencode', key: JSON.stringify(['box', '/other']) },
])('matches both sidebars to a draft on $remoteId before a session exists', async ({ remoteId, platform, key }) => {
  useUiStore.setState({ collapsedProjects: [key] });
  const page = renderSessionPage();
  await flushPromises();
  await act(async () => page.navigate(`/session/new?dir=%2Fother&remoteId=${remoteId}&platform=${encodeURIComponent(platform)}`));
  expect(await screen.findByText('New draft')).toBeInTheDocument();
  const context = JSON.parse(screen.getByTestId('project-context').textContent!);
  expect(context).toMatchObject({ directory: '/other', remoteId, platformId: platform });
  expect(context.session).toBeUndefined();
  expect(useUiStore.getState().collapsedProjects).not.toContain(key);
});
