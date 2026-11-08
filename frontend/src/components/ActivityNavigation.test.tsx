// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { expect, it, vi } from 'vitest';
import { renderWithQuery } from '../test/renderWithQuery';
import { ActivityNavigation } from './ActivityNavigation';
import { resources } from '../lib/resources';

vi.mock('../pages/BackgroundTasks', () => ({ BackgroundTasks: () => <p>Task list</p> }));
vi.mock('../lib/resources', () => ({ resources: vi.fn(() => new Promise(() => {})) }));
vi.mock('../lib/api', () => ({ api: { systemStats: vi.fn(() => new Promise(() => {})) } }));

function Location() {
  return <output aria-label="Location">{useLocation().search}</output>;
}

it('opens a two-tab modal, preserves URL scope and restores focus on close', async () => {
  const onNavigate = vi.fn();
  renderWithQuery(<MemoryRouter initialEntries={['/?remoteId=box']}><ActivityNavigation onNavigate={onNavigate} /><Location /></MemoryRouter>);
  const trigger = screen.getByRole('button', { name: 'Activity' });
  trigger.focus();
  fireEvent.click(trigger);
  expect(onNavigate).toHaveBeenCalledOnce();
  expect(screen.getByRole('dialog', { name: 'Activity' })).toBeVisible();
  expect(screen.getByRole('tab', { name: 'Resources' })).toHaveAttribute('aria-selected', 'true');
  expect(screen.queryByText('Task list')).not.toBeInTheDocument();
  await waitFor(() => expect(resources).toHaveBeenCalled());
  expect(screen.getByLabelText('Location')).toHaveTextContent('remoteId=box&activity=resources');
  const [machine, signal] = vi.mocked(resources).mock.calls.at(-1)!;
  expect(machine).toBe('local');
  fireEvent.mouseDown(screen.getByRole('tab', { name: 'Background tasks' }), { button: 0, ctrlKey: false });
  expect(await screen.findByText('Task list')).toBeVisible();
  expect(signal.aborted).toBe(true);
  fireEvent.click(screen.getByRole('button', { name: 'Close activity' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(screen.getByLabelText('Location')).toHaveTextContent('?remoteId=box');
  expect(trigger).toHaveFocus();
});

it('restores an open resources tab from the URL and closes with Escape', () => {
  renderWithQuery(<MemoryRouter initialEntries={['/?activity=resources']}><ActivityNavigation /></MemoryRouter>);
  expect(screen.getByRole('tab', { name: 'Resources' })).toHaveAttribute('aria-selected', 'true');
  fireEvent.keyDown(window, { key: 'Escape' });
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('offers a phone destination without mounting a second modal', () => {
  renderWithQuery(<MemoryRouter initialEntries={['/sessions?remoteId=box']}><ActivityNavigation triggerOnly /><Location /></MemoryRouter>);
  fireEvent.click(screen.getByRole('link', { name: 'Activity' }));
  expect(screen.getByLabelText('Location')).toHaveTextContent('?remoteId=box&activity=resources');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});
