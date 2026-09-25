// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import { api, type FactoryFormula } from '../lib/api';
import { FactoryConfiguration } from './FactoryConfiguration';

vi.mock('../lib/api', () => ({ api: {
  factoryFormula: vi.fn(), factoryFormulas: vi.fn(), factoryCapacityPolicy: vi.fn(),
  setFactoryCapacityPolicy: vi.fn(), validateFactoryFormula: vi.fn(), previewFactoryFormula: vi.fn(), saveFactoryFormula: vi.fn(),
} }));
vi.mock('./EpicGraph', () => ({ EpicGraph: () => null }));

const formula: FactoryFormula = { id: 'ocman/tracer', version: 3, name: 'Tracer', source: 'name: Tracer\nsteps: []', hash: 'hash', sourceHash: 'source-hash', inputs: [], nodes: [], edges: [], valid: true };

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.factoryFormula).mockResolvedValue(formula);
  vi.mocked(api.factoryFormulas).mockResolvedValue([]);
  vi.mocked(api.factoryCapacityPolicy).mockResolvedValue({ globalCapacity: 10, projectCapacity: 4, projectOverrides: { '/repo': 2 } });
  vi.mocked(api.setFactoryCapacityPolicy).mockImplementation(async (policy) => policy);
  vi.mocked(api.saveFactoryFormula).mockResolvedValue({ ...formula, id: 'custom/test', version: 1 });
});

function renderConfiguration() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><MemoryRouter><FactoryConfiguration /></MemoryRouter></QueryClientProvider>);
}

it('submits named number and multiline capacity fields with native bounds intact', async () => {
  const user = userEvent.setup();
  renderConfiguration();
  const global = await screen.findByLabelText('Global implementation capacity');
  const project = screen.getByLabelText('Default project implementation capacity');
  const overrides = screen.getByLabelText('Project capacity overrides (JSON)');
  for (const field of [global, project]) {
    expect(field).toHaveClass('oc-field');
    expect(field).toHaveAttribute('type', 'number');
    expect(field).toHaveAttribute('min', '1');
    expect(field).toHaveAttribute('max', '1000');
    expect(field).toBeRequired();
  }
  expect(overrides).toHaveClass('oc-field--textarea');
  await user.clear(global);
  await user.type(global, '0');
  await user.click(screen.getByRole('button', { name: 'Save capacity policy' }));
  expect(api.setFactoryCapacityPolicy).not.toHaveBeenCalled();
  await user.clear(global);
  await user.type(global, '6');
  fireEvent.change(overrides, { target: { value: '{\n  "/repo": 3\n}' } });
  await user.click(screen.getByRole('button', { name: 'Save capacity policy' }));
  await waitFor(() => expect(api.setFactoryCapacityPolicy).toHaveBeenCalledWith({ globalCapacity: 6, projectCapacity: 4, projectOverrides: { '/repo': 3 } }));
});

it('preserves readonly source, ID validation and the invalid-source details handler', async () => {
  const user = userEvent.setup();
  renderConfiguration();
  const original = await screen.findByLabelText('Tracer Formula source');
  expect(original).toHaveClass('oc-field--textarea');
  expect(original).toHaveAttribute('readonly');
  expect(original).toHaveAttribute('rows', '15');
  expect(original).toHaveValue(formula.source);
  const id = screen.getByLabelText('Custom Formula ID');
  const source = screen.getByLabelText('Formula YAML');
  expect(id).toHaveClass('oc-field');
  expect(source).toHaveClass('oc-field--textarea');
  expect(id).toHaveAttribute('pattern', 'custom/[a-z][a-z0-9_-]*');
  expect(id).toBeInvalid();
  await user.click(screen.getByRole('button', { name: 'Save immutable revision' }));
  expect(api.saveFactoryFormula).not.toHaveBeenCalled();
  await user.type(id, 'custom/test');
  fireEvent.change(source, { target: { value: '' } });
  await user.click(screen.getByRole('button', { name: 'Validate Formula' }));
  expect(source.closest('details')).toHaveAttribute('open');
  expect(api.validateFactoryFormula).not.toHaveBeenCalled();
  await user.type(source, 'name: Test{Enter}steps: [[]');
  await user.click(screen.getByRole('button', { name: 'Save immutable revision' }));
  await waitFor(() => expect(vi.mocked(api.saveFactoryFormula).mock.calls[0]?.[0]).toEqual({ id: 'custom/test', source: 'name: Test\nsteps: []' }));
});
