// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { SearchSelect } from './SearchSelect';

it('renders custom labels while searching and selecting by the full project path', async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  const path = '/home/user/projects/ocman';
  render(<SearchSelect
    value={path}
    options={[{ value: path, label: path, displayLabel: <span title={path}>ocman</span> }]}
    ariaLabel="Project"
    placeholder="Choose project"
    searchLabel="Search projects"
    onChange={onChange}
  />);

  expect(screen.getByRole('combobox')).toHaveTextContent('ocman');
  expect(screen.getByTitle(path)).toHaveTextContent('ocman');
  await user.click(screen.getByRole('combobox'));
  await user.type(screen.getByRole('textbox'), '/home/user/projects');
  await user.click(screen.getByRole('option', { name: 'ocman' }));
  expect(onChange).toHaveBeenCalledWith(path);
});

it('fuzzy filters and selects an option', async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  render(
    <SearchSelect
      value=""
      options={[
        { value: 'openai/gpt-5', label: 'openai/gpt-5' },
        { value: 'anthropic/claude', label: 'anthropic/claude' },
        { value: 'banana-frontend', label: 'banana-frontend' },
      ]}
      ariaLabel="Model"
      placeholder="Choose model"
      searchLabel="Search models"
      onChange={onChange}
    />,
  );

  await user.click(screen.getByRole('combobox'));
  await user.type(screen.getByRole('textbox', { name: 'Search models' }), 'banfron');
  await user.click(screen.getByRole('option', { name: 'banana-frontend' }));

  expect(onChange).toHaveBeenCalledWith('banana-frontend');
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
});
