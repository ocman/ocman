import { useEffect, useState } from 'react';
import type { PermissionRule } from '../../lib/api.types';
import { sessionPermissionPolicy, type SessionPermissionPolicy } from '../../lib/sessionPermissionPolicy';
import { Button, ButtonGroup, SearchField } from '../Control';
import { Modal } from '../Modal';
import { ModalHeader } from '../ModalHeader';
import { ModalFooter } from '../ModalFooter';
import { PermissionRulesEditor } from '../PermissionRulesEditor';
import './SessionPermissions.css';

export function SessionPermissions({ sessionId, platform }: { sessionId: string; platform: string }) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const show = () => setOpen(true);
    window.addEventListener('oc-session-permissions-open', show);
    return () => window.removeEventListener('oc-session-permissions-open', show);
  }, []);
  return open ? <SessionPermissionsDialog key={`${platform}/${sessionId}`} sessionId={sessionId} platform={platform} onClose={() => setOpen(false)} /> : null;
}

export function SessionPermissionsDialog({ sessionId, platform, onClose }: { sessionId: string; platform: string; onClose: () => void }) {
  const [policy, setPolicy] = useState<SessionPermissionPolicy | null>(null);
  const [rules, setRules] = useState<PermissionRule[]>([]);
  const [search, setSearch] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [loading, setLoading] = useState(true);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let ignore = false;
    sessionPermissionPolicy.get(sessionId, platform).then((result) => {
      if (!ignore) { setPolicy(result); setRules(result.rules); setError(''); }
    }).catch((err: unknown) => { if (!ignore) setError(err instanceof Error ? err.message : 'Could not load session permissions'); })
      .finally(() => { if (!ignore) setLoading(false); });
    return () => { ignore = true; };
  }, [sessionId, platform, reload]);

  const dirty = policy !== null && JSON.stringify(rules) !== JSON.stringify(policy.rules);
  const refresh = () => { setLoading(true); setError(''); setReload((value) => value + 1); };
  const valid = rules.every((rule) => rule.permission.trim() && rule.pattern.trim());
  const close = () => {
    if (!dirty || window.confirm('Discard unsaved session permission rules?')) onClose();
  };
  const save = async () => {
    setSaving(true);
    setError('');
    try {
      const result = await sessionPermissionPolicy.save(sessionId, platform, rules);
      setPolicy(result);
      setRules(result.rules);
    } catch (err) { setError(err instanceof Error ? err.message : 'Could not save session permissions'); }
    finally { setSaving(false); }
  };
  const requests = policy?.requests.filter((request) => `${request.permission} ${request.patterns.join(' ')} ${request.resolution}`.toLowerCase().includes(search.toLowerCase())) ?? [];

  return <Modal label="Session permissions" dialogClassName="oc-modal session-permissions-modal" onClose={close} canClose={!saving}>
    <ModalHeader title="Session permissions" description="Ocman approval rules for this session and its subagents only. Native agent permissions are unchanged." onClose={close} closeLabel="Close session permissions" canClose={!saving} />
    <div className="session-permissions-body">
      {error && <p role="alert">{error}</p>}
      {!policy ? error ? <Button onClick={refresh}>Retry loading permissions</Button> : <p role="status">Loading session permissions...</p> : <>
        <h3>Rules</h3>
        <p>Allow approves matching requests. Ask always waits for you. Deny rejects them. Unmatched requests use existing Ocman approval logic.</p>
        <p>Use * for any text and ? for one character. The last matching rule wins. For example, bash + tea do * + allow.</p>
        <PermissionRulesEditor rules={rules} onChange={setRules} disabled={saving || loading} presets={false} reorderable emptyMessage="No session rules. Ocman's existing approval logic applies." />
        {dirty && <p role="status">Unsaved rules. Review the patterns above, then save to apply them.</p>}
        <h3>Requested permissions</h3>
        <SearchField aria-label="Search requested permissions" value={search} onChange={(event) => setSearch(event.target.value)} />
        <Button disabled={saving || loading || dirty} onClick={refresh}>{loading ? 'Refreshing...' : 'Refresh requests'}</Button>
        <p>Showing up to 2,000 recorded requests. Select an action to add an editable rule, then save.</p>
        {requests.length === 0 && <p>No recorded requests match.</p>}
        <ul className="session-permissions-requests">
          {requests.map((request) => <li key={`${request.sessionId}/${request.permissionId}`}>
            <div><strong>{request.permission}</strong> · {request.resolution}{request.sessionId !== sessionId && ' · Subagent'}</div>
            <time dateTime={new Date(request.requestedAt).toISOString()}>{new Date(request.requestedAt).toLocaleString()}</time>
            {(request.patterns.length ? request.patterns : ['*']).map((pattern, index) => <div key={index} className="session-permissions-request">
              <code>{pattern}</code>
              <ButtonGroup label={`Rule actions for ${pattern}`}>{(['allow', 'ask', 'deny'] as const).map((action) => <Button key={action} disabled={saving || loading} aria-label={`${action} ${pattern}`} onClick={() => setRules([...rules, { permission: request.permission, pattern: escapePattern(pattern), action }])}>{action}</Button>)}</ButtonGroup>
            </div>)}
          </li>)}
        </ul>
      </>}
    </div>
    <ModalFooter label="Session permission actions">
      <Button disabled={saving} onClick={close}>Close</Button>
      <Button variant="accent" disabled={!dirty || !valid || saving || loading} onClick={() => void save()}>{saving ? 'Saving...' : 'Save session rules'}</Button>
    </ModalFooter>
  </Modal>;
}

// A request copied into a rule starts exact; users deliberately add wildcards.
function escapePattern(pattern: string) {
  return pattern.replace(/[\\*?]/g, '\\$&');
}
