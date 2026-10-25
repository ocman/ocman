// @vitest-environment jsdom
import { useState } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { PluginPageFields } from './PluginPageFields';
import { blankPageValues, type PageField, type PageValues } from '../lib/pluginPages';

const fields: PageField[] = [{ key: 'targets', label: 'Targets', type: 'list', fields: [
  { key: 'name', label: 'Name', type: 'text' },
  { key: 'roots', label: 'Directories', type: 'multiline' },
  { key: 'mutate', label: 'Mutations', type: 'boolean' },
  { key: 'credentials', label: 'Credentials', type: 'list', fields: [
    { key: 'user', label: 'Username', type: 'text' }, { key: 'pass', label: 'Password', type: 'secret', help: 'Write only' },
  ] },
] }];

function Form() {
  const [values, setValues] = useState<PageValues>(blankPageValues(fields));
  return <><PluginPageFields fields={fields} values={values} onChange={setValues} /><output>{JSON.stringify(values)}</output></>;
}

describe('Plugin page fields', () => {
  it('edits numeric fields and labeled choices and renders nested validation', () => {
    function TypedForm() {
      const [values, setValues] = useState<PageValues>({ replicas: 2, rows: [{ mode: 'fast' }] });
      return <><PluginPageFields fields={[
        { key: 'replicas', label: 'Replicas', type: 'integer', minimum: 1, maximum: 3, required: true },
        { key: 'rows', label: 'Rows', type: 'list', maxItems: 1, fields: [{ key: 'mode', label: 'Mode', type: 'select', options: [{ value: 'fast', label: 'Fast mode' }] }] },
      ]} values={values} onChange={setValues} issues={[{ path: '/rows/0/mode', message: 'This mode is unavailable.' }]} /><output>{JSON.stringify(values)}</output></>;
    }
    render(<TypedForm />);
    const replicas = screen.getByRole('spinbutton', { name: 'Replicas' });
    expect(replicas).toHaveAttribute('min', '1');
    expect(replicas).toHaveAttribute('step', '1');
    expect(replicas).toBeRequired();
    fireEvent.change(replicas, { target: { value: '3' } });
    expect(screen.getByRole('status')).toHaveTextContent('"replicas":3');
    fireEvent.change(replicas, { target: { value: '' } });
    expect(screen.getByRole('status')).not.toHaveTextContent('replicas');
    expect(screen.getByRole('option', { name: 'Fast mode' })).toHaveValue('fast');
    expect(screen.getByRole('alert')).toHaveTextContent('This mode is unavailable.');
    expect(screen.getByRole('button', { name: 'Add Rows' })).toBeDisabled();
  });
  it('never uses a secret first field as the list heading, including while collapsed', () => {
    function SecretForm() {
      const [values, setValues] = useState<PageValues>({ accounts: [{ password: '' }] });
      return <PluginPageFields fields={[{ key: 'accounts', label: 'Accounts', type: 'list', fields: [
        { key: 'password', label: 'Password', type: 'secret' },
      ] }]} values={values} onChange={setValues} />;
    }
    render(<SecretForm />);
    fireEvent.change(screen.getByLabelText('Accounts 1: Password'), { target: { value: 'private-password' } });
    expect(screen.queryByText('private-password')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('Accounts 1'));
    expect(screen.getByText('Accounts 1')).toBeInTheDocument();
    expect(screen.queryByText('private-password')).not.toBeInTheDocument();
  });
  it('keeps target choices in sync with named rows and shows stale selections', () => {
    function SelectionForm() {
      const [values, setValues] = useState<PageValues>({ selected: 'removed', targets: [{ name: 'staging' }] });
      return <PluginPageFields fields={[
        { key: 'selected', label: 'Target to scan', type: 'select', optionsFrom: 'targets', help: 'Save before scanning' },
        { key: 'targets', label: 'Targets', type: 'list', fields: [{ key: 'name', label: 'Name', type: 'text' }] },
      ]} values={values} onChange={setValues} />;
    }
    render(<SelectionForm />);
    expect(screen.getByRole('option', { name: /no matching target/ })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Targets 1: Name'), { target: { value: 'production' } });
    expect(screen.queryByRole('option', { name: 'staging' })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Target to scan'), { target: { value: 'production' } });
    expect(screen.getByLabelText('Target to scan')).toHaveValue('production');
    expect(screen.getByText('Save before scanning')).toBeInTheDocument();
  });
  it('edits nested target and credential lists without fixed slots', () => {
    render(<Form />);
    fireEvent.click(screen.getByRole('button', { name: 'Add Targets' }));
    fireEvent.change(screen.getByLabelText('Targets 1: Name'), { target: { value: 'staging' } });
    fireEvent.change(screen.getByLabelText('Targets 1: Directories'), { target: { value: '/srv/code' } });
    fireEvent.click(screen.getByLabelText('Targets 1: Mutations'));
    for (let i = 0; i < 5; i++) fireEvent.click(screen.getByRole('button', { name: 'Add Credentials' }));
    const password = screen.getByLabelText('Targets 1: Credentials 5: Password');
    expect(password).toHaveAttribute('type', 'password');
    fireEvent.change(password, { target: { value: 'new-password' } });
    expect(screen.getByRole('status').textContent).toContain('staging');
    fireEvent.click(screen.getByRole('button', { name: 'Remove Credentials 1' }));
    expect(screen.getAllByText('Write only')).toHaveLength(4);
    fireEvent.click(screen.getByRole('button', { name: 'Remove Targets 1' }));
    expect(screen.queryByLabelText('Targets 1: Name')).not.toBeInTheDocument();
  });
});
