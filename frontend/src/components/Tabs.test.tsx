// @vitest-environment jsdom
import { useState } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { Tabs, TabsList, TabsTrigger, TabsContent } from './Tabs';

it('moves focus without mounting a panel until activation, skips disabled tabs, and links panels', async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  render(<Tabs defaultValue="board" onValueChange={onChange}>
    <TabsList aria-label="Views">
      <TabsTrigger value="board">Board</TabsTrigger>
      <TabsTrigger value="disabled" disabled>Unavailable</TabsTrigger>
      <TabsTrigger value="plan">Plan</TabsTrigger>
    </TabsList>
    <TabsContent value="board"><input aria-label="Board draft" /></TabsContent>
    <TabsContent value="plan"><input aria-label="Plan draft" /></TabsContent>
  </Tabs>);
  const board = screen.getByRole('tab', { name: 'Board' });
  const plan = screen.getByRole('tab', { name: 'Plan' });
  const panel = screen.getByRole('tabpanel', { name: 'Board' });
  expect(board).toHaveAttribute('aria-controls', panel.id);
  expect(panel).toHaveAttribute('aria-labelledby', board.id);
  expect(screen.queryByLabelText('Plan draft')).not.toBeInTheDocument();
  await user.tab();
  expect(board).toHaveFocus();
  await user.keyboard('{ArrowRight}');
  await waitFor(() => expect(plan).toHaveFocus());
  expect(board).toHaveAttribute('aria-selected', 'true');
  expect(onChange).not.toHaveBeenCalled();
  expect(screen.queryByLabelText('Plan draft')).not.toBeInTheDocument();
  await user.keyboard('{Enter}');
  expect(onChange).toHaveBeenCalledWith('plan');
  expect(screen.getByRole('tabpanel', { name: 'Plan' })).toHaveAttribute('aria-labelledby', plan.id);
  expect(screen.queryByLabelText('Board draft')).not.toBeInTheDocument();
  await user.keyboard('{ArrowRight}');
  await waitFor(() => expect(board).toHaveFocus());
  await user.keyboard(' ');
  expect(board).toHaveAttribute('aria-selected', 'true');
  await user.keyboard('{End}');
  await waitFor(() => expect(plan).toHaveFocus());
  await user.keyboard('{Home}');
  await waitFor(() => expect(board).toHaveFocus());
});

it('supports controlled automatic activation and caller classes', async () => {
  const user = userEvent.setup();
  function Example() {
    const [value, setValue] = useState('first');
    return <Tabs value={value} onValueChange={setValue} activationMode="automatic">
      <TabsList aria-label="Sections" className="custom-list">
        <TabsTrigger value="first" className="custom-trigger">First</TabsTrigger>
        <TabsTrigger value="second">Second</TabsTrigger>
      </TabsList>
      <TabsContent value="first" className="custom-panel">First content</TabsContent>
      <TabsContent value="second">Second content</TabsContent>
    </Tabs>;
  }
  render(<Example />);
  expect(screen.getByRole('tablist')).toHaveClass('oc-tabs-list', 'custom-list');
  expect(screen.getByRole('tab', { name: 'First' })).toHaveClass('oc-tabs-trigger', 'custom-trigger');
  expect(screen.getByRole('tabpanel')).toHaveClass('oc-tabs-content', 'custom-panel');
  await user.tab();
  await user.keyboard('{ArrowRight}');
  await waitFor(() => expect(screen.getByRole('tab', { name: 'Second' })).toHaveAttribute('aria-selected', 'true'));
  expect(screen.getByRole('tabpanel')).toHaveTextContent('Second content');
});
