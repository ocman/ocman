// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { PermissionRulesEditor } from './PermissionRulesEditor';
import type { PermissionRule } from '../lib/api.types';

describe('PermissionRulesEditor', () => {
  it('renders empty state with preset buttons', () => {
    render(<PermissionRulesEditor rules={[]} onChange={vi.fn()} />);
    expect(screen.getByText('Default')).toBeInTheDocument();
    expect(screen.getByText('Plan only')).toBeInTheDocument();
    expect(screen.getByText('YOLO')).toBeInTheDocument();
    expect(screen.getByText(/No rules/)).toBeInTheDocument();
  });

  it('applies a preset on click', async () => {
    const onChange = vi.fn();
    render(<PermissionRulesEditor rules={[]} onChange={onChange} />);
    await userEvent.click(screen.getByText('Plan only'));
    expect(onChange).toHaveBeenCalledWith([
      { permission: 'edit', pattern: '*', action: 'deny' },
      { permission: 'bash', pattern: '*', action: 'deny' },
    ]);
  });

  it('adds a blank rule', async () => {
    const onChange = vi.fn();
    render(<PermissionRulesEditor rules={[]} onChange={onChange} />);
    await userEvent.click(screen.getByText(/Add rule/));
    expect(onChange).toHaveBeenCalledWith([{ permission: 'bash', pattern: '*', action: 'allow' }]);
  });

  it('removes a rule', async () => {
    const rules: PermissionRule[] = [{ permission: 'bash', pattern: '*', action: 'allow' }];
    const onChange = vi.fn();
    render(<PermissionRulesEditor rules={rules} onChange={onChange} />);
    await userEvent.click(screen.getByLabelText('Remove rule 1'));
    expect(onChange).toHaveBeenCalledWith([]);
  });

  it('edits a rule field', async () => {
    const rules: PermissionRule[] = [{ permission: 'bash', pattern: '*', action: 'allow' }];
    const onChange = vi.fn();
    render(<PermissionRulesEditor rules={rules} onChange={onChange} />);
    const actionSelect = screen.getByLabelText('Rule 1 action');
    await userEvent.selectOptions(actionSelect, 'deny');
    expect(onChange).toHaveBeenCalledWith([{ permission: 'bash', pattern: '*', action: 'deny' }]);
  });

  it('marks yolo preset as active when rules match', () => {
    const rules: PermissionRule[] = [{ permission: '*', pattern: '*', action: 'allow' }];
    render(<PermissionRulesEditor rules={rules} onChange={vi.fn()} />);
    const yoloBtn = screen.getByText('YOLO');
    expect(yoloBtn.className).toContain('perm-rules-preset--active');
  });

  it('shows Custom badge for non-preset rules', () => {
    const rules: PermissionRule[] = [{ permission: 'bash', pattern: 'ls *', action: 'allow' }];
    render(<PermissionRulesEditor rules={rules} onChange={vi.fn()} />);
    expect(screen.getByText('Custom')).toBeInTheDocument();
  });

  it('applying Default preset passes empty array', async () => {
    const onChange = vi.fn();
    const rules: PermissionRule[] = [{ permission: 'bash', pattern: '*', action: 'deny' }];
    render(<PermissionRulesEditor rules={rules} onChange={onChange} />);
    await userEvent.click(screen.getByText('Default'));
    expect(onChange).toHaveBeenCalledWith([]);
  });

  it('disables controls when disabled prop is set', () => {
    render(<PermissionRulesEditor rules={[]} onChange={vi.fn()} disabled />);
    const addBtn = screen.getByText(/Add rule/);
    expect(addBtn).toBeDisabled();
    const presets = screen.getAllByRole('button').filter((b) => b.className.includes('perm-rules-preset'));
    presets.forEach((btn) => expect(btn).toBeDisabled());
  });

  it('shows validation error for empty permission', () => {
    const rules: PermissionRule[] = [{ permission: '', pattern: '*', action: 'allow' }];
    render(<PermissionRulesEditor rules={rules} onChange={vi.fn()} />);
    expect(screen.getByText('Permission is required')).toBeInTheDocument();
    const row = screen.getByRole('listitem');
    expect(row.className).toContain('perm-rules-row--invalid');
  });
});
