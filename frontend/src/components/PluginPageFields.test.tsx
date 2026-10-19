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
