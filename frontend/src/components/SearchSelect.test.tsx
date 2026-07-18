// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { SearchSelect } from './SearchSelect';

it('fuzzy filters and selects an option', async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  render(
    <SearchSelect
      value=""
      options={[
        { value: 'openai/gpt-5', label: 'openai/gpt-5' },
        { value: 'anthropic/claude', label: 'anthropic/claude' },
      ]}
      ariaLabel="Model"
      placeholder="Choose model"
      searchLabel="Search models"
      onChange={onChange}
    />,
  );

  await user.click(screen.getByRole('combobox'));
  await user.type(screen.getByRole('textbox', { name: 'Search models' }), 'opnai');
  await user.click(screen.getByRole('option', { name: 'openai/gpt-5' }));

  expect(onChange).toHaveBeenCalledWith('openai/gpt-5');
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
});
