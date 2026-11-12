import { useEffect, useState } from 'react';
import * as Toast from '@radix-ui/react-toast';
import { BUILD_REFRESH_MS, watchBuildUpdate } from '../lib/buildUpdate';
import { canAutomaticallyReload } from '../lib/automaticReload';
import { claimBuildReload } from '../lib/buildReloadBudget';
import { Button } from './Control';
import { PromptToast, PromptToastActions, PromptToastBody, PromptToastHeading, PromptToastViewport } from './PromptToast';

const reloadPage = () => window.location.reload();
const loadedHash = import.meta.env.MODE === 'test' ? '' : typeof __OCMAN_BUILD_HASH__ === 'string' ? __OCMAN_BUILD_HASH__ : '';

export function BuildUpdateNotice({ buildHash = loadedHash, reload = reloadPage }: {
  buildHash?: string;
  reload?: () => void;
}) {
  const [update, setUpdate] = useState<{ hash: string; deadline: number } | null>(null);
  const [seconds, setSeconds] = useState(60);
  const [deferred, setDeferred] = useState<'drafts' | 'retries' | null>(null);
  useEffect(() => {
    if (!buildHash || import.meta.env.DEV && !import.meta.env.MODE.startsWith('test')) return;
    return watchBuildUpdate(buildHash, hash => setUpdate({ hash, deadline: Date.now() + BUILD_REFRESH_MS }));
  }, [buildHash]);
  useEffect(() => {
    if (update === null) return;
    let refreshed = false;
    const tick = () => {
      const left = Math.max(0, Math.ceil((update.deadline - Date.now()) / 1000));
      setSeconds(left);
      if (!left && !refreshed) {
        refreshed = true;
        if (!canAutomaticallyReload()) setDeferred('drafts');
        else if (!claimBuildReload(buildHash, update.hash)) setDeferred('retries');
        else reload();
      }
    };
    tick();
    const timer = setInterval(tick, 1000);
    document.addEventListener('visibilitychange', tick);
    return () => { clearInterval(timer); document.removeEventListener('visibilitychange', tick); };
  }, [update, reload, buildHash]);
  if (update === null) return null;
  return <Toast.Provider duration={Infinity}>
    <PromptToast open duration={Infinity} onEscapeKeyDown={event => event.preventDefault()}>
      <PromptToastHeading>Update available</PromptToastHeading>
      <PromptToastBody>{deferred === 'drafts' ? 'A new version of Ocman is available. Automatic refresh paused to keep your drafts and unsaved changes. Save or send them before refreshing.' : deferred === 'retries' ? 'Automatic refresh paused after an earlier update attempt. Refresh manually when ready.' : <>A new version of Ocman is available. Refreshing automatically in {seconds} {seconds === 1 ? 'second' : 'seconds'}. Save any unsaved changes now.</>}</PromptToastBody>
      <PromptToastActions label="Application update">
        <Button type="button" size="small" variant="accent" onClick={reload}>Refresh now</Button>
      </PromptToastActions>
    </PromptToast>
    <PromptToastViewport />
  </Toast.Provider>;
}
