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

it('caps rendered options so huge catalogs open instantly', async () => {
  const user = userEvent.setup();
  const options = Array.from({ length: 1000 }, (_, i) => ({ value: `p/m${i}`, label: `p/m${i}` }));
  render(<SearchSelect value="" options={options} ariaLabel="Model" placeholder="Model" searchLabel="Search" onChange={vi.fn()} />);
  await user.click(screen.getByRole('combobox'));
  expect(screen.getAllByRole('option')).toHaveLength(200);
  expect(screen.getByText('Type to search 800 more…')).toBeInTheDocument();
  await user.type(screen.getByRole('textbox'), 'm999');
  expect(screen.getByRole('option', { name: 'p/m999' })).toBeInTheDocument();
});

it('uses phrasing content only, so it can render inside a paragraph', async () => {
  const user = userEvent.setup();
  const { container } = render(
    <p>
      <SearchSelect
        value=""
        options={[{ value: 'a', label: 'a', section: 'Group' }]}
        ariaLabel="Model"
        placeholder="Choose model"
        searchLabel="Search models"
        onChange={vi.fn()}
      />
    </p>,
  );
  await user.click(screen.getByRole('combobox'));
  expect(container.querySelector('p div')).toBeNull();
});

it('renders disabled options as unselectable', async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  render(<SearchSelect value="a" options={[{ value: 'a', label: 'A' }, { value: 'b', label: 'B', disabled: true }]}
    ariaLabel="Pick" placeholder="Pick" searchLabel="Search" className="extra" onChange={onChange} />);
  expect(screen.getByRole('combobox').parentElement).toHaveClass('oc-search-select', 'extra');
  await user.click(screen.getByRole('combobox'));
  expect(screen.getByRole('option', { name: 'B' })).toBeDisabled();
  await user.click(screen.getByRole('option', { name: 'B' }));
  expect(onChange).not.toHaveBeenCalled();
});
