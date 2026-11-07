import { useEffect, useState } from 'react';
import * as Toast from '@radix-ui/react-toast';
import { BUILD_REFRESH_MS, watchBuildUpdate } from '../lib/buildUpdate';
import { Button } from './Control';
import { PromptToast, PromptToastActions, PromptToastBody, PromptToastHeading, PromptToastViewport } from './PromptToast';

const reloadPage = () => window.location.reload();
const loadedHash = import.meta.env.MODE === 'test' ? '' : typeof __OCMAN_BUILD_HASH__ === 'string' ? __OCMAN_BUILD_HASH__ : '';

export function BuildUpdateNotice({ buildHash = loadedHash, reload = reloadPage }: {
  buildHash?: string;
  reload?: () => void;
}) {
  const [deadline, setDeadline] = useState<number | null>(null);
  const [seconds, setSeconds] = useState(60);
  useEffect(() => {
    if (!buildHash || import.meta.env.DEV && !import.meta.env.MODE.startsWith('test')) return;
    return watchBuildUpdate(buildHash, () => setDeadline(Date.now() + BUILD_REFRESH_MS));
  }, [buildHash]);
  useEffect(() => {
    if (deadline === null) return;
    let refreshed = false;
    const tick = () => {
      const left = Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
      setSeconds(left);
      if (!left && !refreshed) { refreshed = true; reload(); }
    };
    tick();
    const timer = setInterval(tick, 1000);
    document.addEventListener('visibilitychange', tick);
    return () => { clearInterval(timer); document.removeEventListener('visibilitychange', tick); };
  }, [deadline, reload]);
  if (deadline === null) return null;
  return <Toast.Provider duration={Infinity}>
    <PromptToast open duration={Infinity} onEscapeKeyDown={event => event.preventDefault()}>
      <PromptToastHeading>Update available</PromptToastHeading>
      <PromptToastBody>A new version of Ocman is available. Refreshing automatically in {seconds} {seconds === 1 ? 'second' : 'seconds'}. Save any unsaved changes now.</PromptToastBody>
      <PromptToastActions label="Application update">
        <Button type="button" size="small" variant="accent" onClick={reload}>Refresh now</Button>
      </PromptToastActions>
    </PromptToast>
    <PromptToastViewport />
  </Toast.Provider>;
}
