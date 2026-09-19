import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { APIError } from '../lib/api';
import { useApiStore } from '../lib/apiStore';
import { pluginActions, type PluginActionRequest, type PluginActionResponse } from '../lib/plugins';
import { Modal } from './Modal';

const ERRORS: Record<string, string> = {
  permission_denied: 'This action is denied. Check the plugin’s grants and enablement.',
  unavailable: 'This plugin is unavailable.',
  deadline_exceeded: 'This action timed out. It may have completed; it was not retried.',
  conflict: 'This action could not be repeated safely. It was not retried.',
};

export function PluginActionDialog({ label, request: initialRequest, onClose }: {
  label: string;
  request: PluginActionRequest;
  onClose: () => void;
}) {
  const [request, setRequest] = useState(initialRequest);
  const [response, setResponse] = useState<PluginActionResponse | null>(null);
  const [error, setError] = useState('');
  const headingRef = useRef<HTMLHeadingElement>(null);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const refreshSessions = useApiStore((s) => s.refreshCachedSessions);
  const pending = useRef<{ request: PluginActionRequest; promise: Promise<PluginActionResponse>; handled: boolean } | null>(null);

  useEffect(() => {
    let ignore = false;
    // Share the request across effect replays. Never automatically repeat an effectful call.
    if (pending.current?.request !== request) {
      pending.current = { request, promise: pluginActions.invoke(request, AbortSignal.timeout(35_000)), handled: false };
    }
    const operation = pending.current;
    operation.promise.then((result) => {
      if (ignore || operation.handled) return;
      operation.handled = true;
      setResponse(result);
      if (result.error) setError(ERRORS[result.error.category] ?? 'The action failed. It was not retried.');
      for (const item of result.results ?? []) {
        if (item.kind === 'navigation') navigate(`/${item.target}`);
        if (item.kind === 'refresh') {
          void queryClient.invalidateQueries({ queryKey: [item.target === 'actions' ? 'plugin-actions' : item.target] });
          if (item.target === 'sessions') void refreshSessions().catch(() => {});
        }
      }
    }).catch((err: unknown) => {
      if (ignore) return;
      const category = err instanceof APIError
        ? ({ 403: 'permission_denied', 503: 'unavailable', 504: 'deadline_exceeded' } as Record<number, string>)[err.status]
        : err instanceof DOMException && err.name === 'TimeoutError' ? 'deadline_exceeded' : undefined;
      setError(category ? ERRORS[category] : 'The action failed. It was not retried.');
    });
    return () => { ignore = true; };
  }, [request, navigate, queryClient, refreshSessions]);

  const confirmation = response?.confirmation;
  const running = !response && !error;
  return (
    <Modal label={label} onClose={onClose} canClose={!running} backdropClassName="oc-cmd-backdrop" dialogClassName="oc-cmd-palette">
      <div className="oc-cmd-action-body">
        <h2 ref={headingRef} tabIndex={-1}>{label}</h2>
        {error ? <p role="alert">{error}</p> : confirmation ? <p role="status">{confirmation.text}</p> : <>
          <p role="status">{response ? 'Action completed.' : 'Running action…'}</p>
          {response?.results?.map((item, index) => {
            switch (item.kind) {
              case 'notice': return <p key={index}>{item.text}</p>;
              case 'link': return <p key={index}><a href={item.url} target="_blank" rel="noopener noreferrer">{item.label}</a></p>;
              case 'artifact': return <p key={index}><a href={pluginActions.artifactURL(request.ownerId, item.handle)} download={item.label}>{item.label}</a></p>;
              default: return null;
            }
          })}
        </>}
        {!running && <button type="button" onClick={onClose}>{confirmation ? 'Cancel' : 'Close'}</button>}
        {confirmation && <button type="button" onClick={() => {
          headingRef.current?.focus();
          setResponse(null);
          setRequest({ ...request, confirmationToken: confirmation.token });
        }}>Confirm</button>}
      </div>
    </Modal>
  );
}
