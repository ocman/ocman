import type { ComponentProps } from 'react';
import type { useSettingSave } from '../lib/useSaveStatus';
import { ModelSelect } from './ModelSelect';
import { SaveStatus } from './SaveStatus';

/** Model selection with the same explicit save feedback as other settings. */
export function SettingModelSelect({ save, onSave, ...props }: Omit<ComponentProps<typeof ModelSelect>, 'onChange'> & {
  save: ReturnType<typeof useSettingSave>;
  onSave: (model: string) => void | Promise<unknown>;
}) {
  return <div>
    <ModelSelect {...props} disabled={props.disabled || save.state === 'saving'} onChange={(model) => {
      if (model !== props.value) void save.track(async () => onSave(model)).catch(() => {});
    }} />
    <SaveStatus state={save.state} />
  </div>;
}
