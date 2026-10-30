import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, SelectField, TextField } from '../Control';
import { Modal } from '../Modal';
import { ModalHeader } from '../ModalHeader';
import { ModalFooter } from '../ModalFooter';
import { prWorkflowApi } from '../../lib/prWorkflowApi';
import type { Upstream } from '../../lib/upstreamApi';
import './SessionPullRequests.css';

interface Props {
  sessionId: string;
  platform: string;
  upstreams: Upstream[];
  onClose: () => void;
  onLinked: () => void;
}

export function LinkPullRequestModal({ sessionId, platform, upstreams, onClose, onLinked }: Props) {
  const [remote, setRemote] = useState(upstreams[0]?.remote ?? '');
  const [number, setNumber] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const selectedRemote = upstreams.some((upstream) => upstream.remote === remote) ? remote : upstreams[0]?.remote ?? '';
  const validNumber = Number.isInteger(Number(number)) && Number(number) > 0;
  const submit = async () => {
    if (busy || !selectedRemote || !validNumber) return;
    setBusy(true);
    setError('');
    try {
      await prWorkflowApi.change(sessionId, platform, { action: 'link', number: Number(number), remote: selectedRemote });
      onLinked();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not link pull request.');
    } finally {
      setBusy(false);
    }
  };
  return <Modal label="Link pull request" onClose={onClose} canClose={!busy}>
    <ModalHeader title="Link pull request" closeLabel="Close link pull request" onClose={onClose} canClose={!busy} />
    <form className="oc-session-pr-link" onSubmit={(event) => { event.preventDefault(); void submit(); }}>
      <label className="oc-session-pr-link-field">Upstream
      <SelectField aria-label="PR upstream" value={selectedRemote} disabled={busy || !upstreams.length} onChange={(event) => setRemote(event.target.value)}>
        {upstreams.map((upstream) => <option key={upstream.remote} value={upstream.remote}>{upstream.remote} · {upstream.repo}</option>)}
      </SelectField>
      </label>
      <label className="oc-session-pr-link-field">PR number
      <TextField aria-label="PR number to link" data-autofocus type="number" min="1" step="1" required value={number} onChange={(event) => setNumber(event.target.value)} placeholder="e.g. 42" disabled={busy} />
      </label>
      {!upstreams.length && <p>No supported upstream detected for this session.</p>}
      <p className="oc-session-pr-meta"><Link to="/settings?tab=connections">Source-control setup</Link></p>
      {error && <p role="alert">{error}</p>}
      <ModalFooter label="Link pull request actions">
        <Button onClick={onClose} disabled={busy}>Cancel</Button>
        <Button type="submit" variant="accent" disabled={busy || !selectedRemote || !validNumber}>{busy ? 'Linking…' : 'Link PR'}</Button>
      </ModalFooter>
    </form>
  </Modal>;
}
