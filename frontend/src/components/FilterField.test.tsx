// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it } from 'vitest';
import { SelectField } from './Control';
import { FilterField } from './FilterField';

it('keeps a captioned compact filter named, focusable and selectable', async () => {
  const user = userEvent.setup();
  const { rerender } = render(<FilterField label="Last" compact><SelectField aria-label="Last"><option value="7">7 days</option><option value="30">30 days</option></SelectField></FilterField>);
  const select = screen.getByRole('combobox', { name: 'Last' });
  await user.tab();
  expect(select).toHaveFocus();
  await user.selectOptions(select, '30');
  expect(select).toHaveValue('30');
  expect(screen.getByText('Last', { exact: true })).toBeVisible();
  rerender(<FilterField><SelectField aria-label="Last"><option value="7">7 days</option></SelectField></FilterField>);
  expect(screen.queryByText('Last', { exact: true })).not.toBeInTheDocument();
  expect(screen.getByRole('combobox', { name: 'Last' })).toBeInTheDocument();
});
