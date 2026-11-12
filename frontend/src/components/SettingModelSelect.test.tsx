// @vitest-environment jsdom
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { useSettingSave } from '../lib/useSaveStatus';
import { SettingModelSelect } from './SettingModelSelect';

it('saves the selected named account with its model and shows failures', async () => {
  const save = vi.fn().mockRejectedValue(new Error('failed'));
  function Form() {
    const status = useSettingSave();
    return <SettingModelSelect value="" models={[]} modelEntries={[
      { platform: 'cnx_home', platformName: 'Home', provider: 'p', model: 'same' },
      { platform: 'cnx_work', platformName: 'Work', provider: 'p', model: 'same' },
    ]} save={status} onSave={save} />;
  }
  render(<Form />);
  const user = userEvent.setup();
  await user.click(screen.getByRole('combobox', { name: 'Model' }));
  await user.click(screen.getByRole('tab', { name: 'Work' }));
  await user.click(screen.getByRole('option', { name: /p\/same/ }));
  await waitFor(() => expect(save).toHaveBeenCalledWith('cnx_work:p/same'));
  expect(await screen.findByLabelText('Save failed')).toBeInTheDocument();
});
