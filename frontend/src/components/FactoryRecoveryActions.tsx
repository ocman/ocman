import { useState } from 'react';
import { Link } from 'react-router-dom';
import type { FactoryAttempt, FactoryRecoveryGate } from '../lib/api';
import { useResolveFactoryRecoveryGate } from '../lib/queries';
import { Button, SelectField, TextField } from './Control';
import './FactoryEpicCard.css';

export function FactoryRecoveryActions({ gate, attempts, inbox = false }: { gate: FactoryRecoveryGate; attempts?: FactoryAttempt[]; inbox?: boolean }) {
  const resolve = useResolveFactoryRecoveryGate();
  const [response, setResponse] = useState(gate.response ?? gate.choices?.[0] ?? '');
  const pending = gate.resolution === 'resume_pending';
  const answer = pending ? gate.response ?? response : response;
  const actions = pending ? ['resume'] as const : ['resume', 'retry', 'cancel'] as const;
  const labels = inbox ? { resume: pending ? 'Retry resume' : 'Resume', retry: 'Retry', cancel: 'Cancel work' } : { resume: 'Resume work', retry: 'Retry work', cancel: 'Cancel work' };
  const busyLabels = { resume: 'Resuming…', retry: 'Retrying…', cancel: 'Cancelling…' };
  const session = attempts?.find((attempt) => attempt.id === gate.attemptId)?.session;
  const label = inbox ? `Recovery response for ${gate.issueId}` : 'Recovery response';
  return <span className="oc-factory-action-issue">
    {!inbox && <><strong>{gate.question}</strong><span>{gate.reason}</span></>}
    {session?.id && <Link to={`/session/${encodeURIComponent(session.id)}?factoryEpic=${encodeURIComponent(gate.epicId)}`}>Inspect recovery session</Link>}
    <label>Recovery response{gate.choices?.length ? <SelectField aria-label={label} value={answer} disabled={pending} onChange={(event) => setResponse(event.target.value)}>{gate.choices.map((choice) => <option key={choice}>{choice}</option>)}</SelectField> : <TextField aria-label={label} value={answer} disabled={pending} onChange={(event) => setResponse(event.target.value)} />}</label>
    <span className="oc-factory-action-buttons">{actions.map((action) => <Button key={action} type="button" variant={inbox && action === 'resume' ? 'accent' : 'default'} disabled={resolve.isPending || resolve.isSuccess} onClick={() => resolve.mutate({ id: gate.issueId, action, response: action === 'resume' ? answer : '' })}>{resolve.isPending && resolve.variables?.action === action ? busyLabels[action] : labels[action]}</Button>)}</span>
    {resolve.isPending && <span role="status">Saving recovery decision…</span>}
    {resolve.isSuccess && <span role="status">Recovery decision saved.</span>}
    {resolve.isError && <span role="alert">{resolve.error.message}</span>}
  </span>;
}
