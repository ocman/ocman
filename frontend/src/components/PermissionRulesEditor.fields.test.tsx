// @vitest-environment jsdom
import { useState } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { PermissionRulesEditor } from './PermissionRulesEditor';
import type { PermissionRule } from '../lib/api.types';

it('edits shared text fields and marks only the invalid permission or pattern', async () => {
  const user = userEvent.setup();
  function Editor() {
    const [rules, setRules] = useState<PermissionRule[]>([{ permission: '', pattern: '*', action: 'allow' }]);
    return <PermissionRulesEditor rules={rules} onChange={setRules} />;
  }
  render(<Editor />);
  const permission = screen.getByLabelText('Rule 1 permission');
  const pattern = screen.getByLabelText('Rule 1 pattern');
  expect(permission).toHaveClass('oc-field');
  expect(pattern).toHaveClass('oc-field');
  expect(permission).toHaveAttribute('list', 'perm-permissions-0');
  expect(permission).toBeInvalid();
  expect(pattern).toBeValid();
  await user.type(permission, 'bash');
  expect(permission).toBeValid();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  await user.clear(pattern);
  expect(pattern).toBeInvalid();
  expect(permission).toBeValid();
  expect(screen.getByRole('alert')).toHaveTextContent('Pattern is required');
  await user.type(pattern, 'git *');
  expect(pattern).toHaveValue('git *');
  expect(pattern).toBeValid();
});

it('retains invalid state on disabled rules without allowing edits', async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  render(<PermissionRulesEditor disabled rules={[{ permission: '', pattern: '', action: 'unknown' as PermissionRule['action'] }]} onChange={onChange} />);
  for (const label of ['Rule 1 permission', 'Rule 1 pattern', 'Rule 1 action']) {
    const field = screen.getByLabelText(label);
    expect(field).toHaveClass('oc-field');
    expect(field).toBeDisabled();
    expect(field).toHaveAttribute('aria-invalid', 'true');
  }
  await user.type(screen.getByLabelText('Rule 1 permission'), 'bash');
  await user.selectOptions(screen.getByLabelText('Rule 1 action'), 'allow');
  expect(onChange).not.toHaveBeenCalled();
});
