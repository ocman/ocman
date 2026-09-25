// @vitest-environment jsdom
import { createRef } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { SearchField, SelectField, TextareaField, TextField } from './Control';

it('forwards input types, constraints, refs, classes and named form values', async () => {
  const user = userEvent.setup();
  const ref = createRef<HTMLInputElement>();
  const onChange = vi.fn();
  render(<form aria-label="Settings">
    <TextField ref={ref} aria-label="Count" name="count" type="number" min={1} max={10} step={2} required defaultValue={3} className="custom" onChange={onChange} />
    <TextField aria-label="Secret" name="secret" type="password" autoComplete="new-password" defaultValue="token" />
    <TextField aria-label="Title" name="title" defaultValue="Title" readOnly />
  </form>);
  const count = screen.getByRole('spinbutton');
  expect(ref.current).toBe(count);
  expect(count).toHaveClass('oc-field', 'custom');
  expect(count).toHaveAttribute('min', '1');
  expect(count).toHaveAttribute('max', '10');
  expect(count).toHaveAttribute('step', '2');
  expect(count).toBeRequired();
  await user.clear(count);
  expect(count).toBeInvalid();
  await user.type(count, '5');
  expect(count).toBeValid();
  expect(onChange).toHaveBeenCalled();
  expect(screen.getByLabelText('Secret')).toHaveAttribute('type', 'password');
  expect(screen.getByLabelText('Secret')).toHaveAttribute('autocomplete', 'new-password');
  await user.type(screen.getByLabelText('Title'), 'ignored');
  expect(Object.fromEntries(new FormData(screen.getByRole('form') as HTMLFormElement))).toEqual({ count: '5', secret: 'token', title: 'Title' });
});

it('forwards textarea rows, validation, ref and multiline changes', async () => {
  const user = userEvent.setup();
  const ref = createRef<HTMLTextAreaElement>();
  const invalid = vi.fn();
  const change = vi.fn();
  render(<form aria-label="Message">
    <TextareaField ref={ref} aria-label="Body" name="body" rows={6} cols={40} required maxLength={100} placeholder="Write here" onInvalid={invalid} onChange={change} className="editor" />
  </form>);
  const body = screen.getByRole('textbox');
  expect(ref.current).toBe(body);
  expect(body).toHaveClass('oc-field', 'oc-field--textarea', 'editor');
  expect(body).toHaveAttribute('rows', '6');
  expect(body).toHaveAttribute('cols', '40');
  expect(body).toHaveAttribute('maxlength', '100');
  expect(body).toHaveAttribute('placeholder', 'Write here');
  expect(ref.current!.reportValidity()).toBe(false);
  expect(invalid).toHaveBeenCalled();
  await user.type(body, 'first{Enter}second');
  expect(change).toHaveBeenCalled();
  expect(new FormData(screen.getByRole('form') as HTMLFormElement).get('body')).toBe('first\nsecond');
});

it('keeps invalid fields focusable, skips disabled fields and submits readonly textareas', async () => {
  const user = userEvent.setup();
  const change = vi.fn();
  render(<form aria-label="States">
    <TextField aria-label="Invalid input" aria-invalid="true" aria-describedby="error" />
    <p id="error">Fix the input</p>
    <TextField aria-label="Disabled input" name="disabledInput" disabled aria-invalid="true" onChange={change} />
    <TextareaField aria-label="Disabled textarea" name="disabledTextarea" disabled aria-invalid="true" onChange={change} />
    <TextareaField aria-label="Invalid textarea" aria-invalid="grammar" />
    <TextareaField aria-label="Readonly textarea" name="readonly" readOnly defaultValue="kept" />
  </form>);
  await user.tab();
  expect(screen.getByLabelText('Invalid input')).toHaveFocus();
  expect(screen.getByLabelText('Invalid input')).toHaveAccessibleDescription('Fix the input');
  await user.tab();
  expect(screen.getByLabelText('Invalid textarea')).toHaveFocus();
  for (const label of ['Disabled input', 'Disabled textarea']) {
    const field = screen.getByLabelText(label);
    expect(field).toBeDisabled();
    expect(field).toHaveAttribute('aria-invalid', 'true');
    await user.type(field, 'ignored');
  }
  expect(change).not.toHaveBeenCalled();
  await user.type(screen.getByLabelText('Readonly textarea'), 'ignored');
  expect(Object.fromEntries(new FormData(screen.getByRole('form') as HTMLFormElement))).toEqual({ readonly: 'kept' });
});

it('preserves SearchField and SelectField native behavior', async () => {
  const user = userEvent.setup();
  const change = vi.fn();
  render(<>
    <SearchField aria-label="Search" type="text" className="filter" />
    <SelectField aria-label="Choice" defaultValue="one" onChange={change}><option>one</option><option>two</option></SelectField>
  </>);
  expect(screen.getByRole('searchbox')).toHaveClass('oc-field', 'oc-field--search', 'filter');
  await user.type(screen.getByRole('searchbox'), 'query');
  expect(screen.getByRole('searchbox')).toHaveValue('query');
  await user.selectOptions(screen.getByRole('combobox'), 'two');
  expect(screen.getByRole('combobox')).toHaveValue('two');
  expect(change).toHaveBeenCalled();
});
