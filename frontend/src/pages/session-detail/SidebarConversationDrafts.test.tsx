// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, expect, it } from 'vitest';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { SidebarConversationDrafts } from './SidebarConversationDrafts';
import { rememberConversationDraft, useNewConversationDrafts } from '../../lib/newConversationDrafts';
import { newSessionPath } from '../../lib/newSessionPath';

beforeEach(() => {
  localStorage.clear();
  useNewConversationDrafts.setState({ drafts: [] });
});

function Location() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname}{location.search}</output>;
}

function mount(searchQuery = '', path = '/') {
  return render(<MemoryRouter initialEntries={[path]}><SidebarConversationDrafts searchQuery={searchQuery} /><Location /></MemoryRouter>);
}

it('shows empty prepared conversations and navigates with the same draft identity and owner', () => {
  rememberConversationDraft({ draftId: 'a', directory: '/repo' });
  rememberConversationDraft({ draftId: 'b', directory: '/repo', remoteId: 'box', platform: 'r-box:opencode' });
  mount();
  fireEvent.click(screen.getByRole('button', { name: /New session 2/ }));
  expect(screen.getByTestId('location')).toHaveTextContent('/session/new?dir=%2Frepo&remoteId=box&platform=r-box%3Aopencode&draftId=b');
  expect(screen.getByRole('button', { name: /New session 2/ })).toHaveAttribute('aria-current', 'page');
  fireEvent.click(screen.getAllByRole('button', { name: 'Discard draft' })[1]);
  expect(screen.getByTestId('location')).toHaveTextContent('/session/new?dir=%2Frepo&draftId=a');
  fireEvent.click(screen.getByRole('button', { name: 'Discard draft' }));
  expect(screen.getByTestId('location')).toHaveTextContent('/');
  expect(screen.queryByLabelText('Prepared sessions')).not.toBeInTheDocument();
});

it('filters drafts by project, title and owner, while keeping the selected draft visible', () => {
  rememberConversationDraft({ draftId: 'a', directory: '/first', title: 'Login' });
  rememberConversationDraft({ draftId: 'b', directory: '/second', title: 'Build', remoteId: 'box' });
  const view = mount('box');
  expect(screen.queryByRole('button', { name: /Login/ })).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: /Build/ })).toBeInTheDocument();
  view.unmount();
  mount('missing', newSessionPath({ draftId: 'a', directory: '/first' }));
  expect(screen.getByRole('button', { name: /Login/ })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Build/ })).not.toBeInTheDocument();
});

it('renders no section when the search matches no drafts', () => {
  rememberConversationDraft({ draftId: 'a', directory: '/repo' });
  mount('missing');
  expect(screen.queryByLabelText('Prepared sessions')).not.toBeInTheDocument();
});
