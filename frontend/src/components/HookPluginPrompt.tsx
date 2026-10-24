import { useState } from 'react';
import * as Toast from '@radix-ui/react-toast';
import { useHookPlugin } from '../lib/useHookPlugin';
import { readStorage, writeStorage } from '../lib/storage';
import { Button } from './Control';
import { PromptToast, PromptToastClose, PromptToastHeading, PromptToastBody, PromptToastActions, PromptToastViewport } from './PromptToast';

const DISMISS_STORAGE_KEY = 'ocman.hook-plugin-dismissed';

/**
 * HookPluginPrompt offers, once, to install the hook plugin that gives
 * terminal-started sessions live status (FR-6). Settings keeps the button.
 */
export function HookPluginPrompt() {
  const { status, error, set } = useHookPlugin();
  const [dismissed, setDismissed] = useState(() => readStorage(DISMISS_STORAGE_KEY) === '1');
  const [installing, setInstalling] = useState(false);
  if (!status?.available || status.installed || dismissed) return null;

  function dismiss() {
    writeStorage(DISMISS_STORAGE_KEY, '1');
    setDismissed(true);
  }

  return (
    <Toast.Provider swipeDirection="right" duration={Infinity}>
      <PromptToast data-kind="permission" open duration={Infinity}
        onOpenChange={(open) => { if (!open) dismiss(); }}>
        <PromptToastClose label="Not now" />
        <PromptToastHeading>Live status for terminal sessions</PromptToastHeading>
        <PromptToastBody data-testid="hook-plugin-prompt">
          Install ocman's Claude Code plugin so sessions started in a terminal show when they are busy?
        </PromptToastBody>
        {error && <PromptToastBody role="alert">{error}</PromptToastBody>}
        <PromptToastActions label="Hook plugin setup actions">
          <Toast.Action asChild altText="Install the ocman Claude plugin" onClick={(e) => {
            e.preventDefault();
            setInstalling(true);
            void set(true).finally(() => setInstalling(false));
          }}>
            <Button type="button" size="small" variant="accent" disabled={installing}>
              {installing ? 'Installing…' : 'Install'}
            </Button>
          </Toast.Action>
        </PromptToastActions>
      </PromptToast>
      <PromptToastViewport />
    </Toast.Provider>
  );
}
