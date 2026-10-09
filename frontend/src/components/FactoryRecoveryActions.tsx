import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import type { FactoryAttempt, FactoryRecoveryGate } from '../lib/api';
import { useResolveFactoryRecoveryGate } from '../lib/queries';
import { Button, SelectField, TextField } from './Control';
import { FactoryActionText } from './FactoryActionText';
import './FactoryEpicCard.css';

export function FactoryRecoveryActions({ gate, attempts, inbox = false }: { gate: FactoryRecoveryGate; attempts?: FactoryAttempt[]; inbox?: boolean }) {
  const resolve = useResolveFactoryRecoveryGate();
  useResetOnGateChange(gate, resolve);
  const [response, setResponse] = useState(gate.response ?? gate.choices?.[0] ?? '');
  const pending = gate.resolution === 'resume_pending';
  const answer = pending ? gate.response ?? response : response;
  const actions = pending ? ['resume'] as const : ['resume', 'retry', 'cancel'] as const;
  const labels = inbox ? { resume: pending ? 'Retry resume' : 'Resume', retry: 'Retry', cancel: 'Cancel work' } : { resume: 'Resume work', retry: 'Retry work', cancel: 'Cancel work' };
  const busyLabels = { resume: 'Resuming…', retry: 'Retrying…', cancel: 'Cancelling…' };
  const session = attempts?.find((attempt) => attempt.id === gate.attemptId)?.session;
  const label = inbox ? `Recovery response for ${gate.issueId}` : 'Recovery response';
  return <div className="oc-factory-action-issue">
    {!inbox && <FactoryActionText text={[gate.question, gate.reason].filter(Boolean).join('\n\n')} />}
    {session?.id && <Link to={`/session/${encodeURIComponent(session.id)}?factoryEpic=${encodeURIComponent(gate.epicId)}`}>Inspect recovery session</Link>}
    <label>Recovery response{gate.choices?.length ? <SelectField aria-label={label} value={answer} disabled={pending} onChange={(event) => setResponse(event.target.value)}>{gate.choices.map((choice) => <option key={choice}>{choice}</option>)}</SelectField> : <TextField aria-label={label} value={answer} disabled={pending} onChange={(event) => setResponse(event.target.value)} />}</label>
    <span className="oc-factory-action-buttons">{actions.map((action) => <Button key={action} type="button" variant={inbox && action === 'resume' ? 'accent' : 'default'} disabled={resolve.isPending || resolve.isSuccess} onClick={() => resolve.mutate({ id: gate.issueId, action, response: action === 'resume' ? answer : '' })}>{resolve.isPending && resolve.variables?.action === action ? busyLabels[action] : labels[action]}</Button>)}</span>
    <RecoveryStatus gate={resolve.data ?? gate} pending={resolve.isPending} saved={resolve.isSuccess} />
    {resolve.isError && <span role="alert">{resolve.error.message}</span>}
  </div>;
}

function RecoveryStatus({ gate, pending, saved }: { gate: FactoryRecoveryGate; pending: boolean; saved: boolean }) {
  if (pending) return <span role="status">Saving recovery decision…</span>;
  // An open gate carrying a response is a resume waiting for the Epic workspace.
  if (gate.resolution === 'open' && gate.response) return <span role="status">Resume queued. It continues automatically once the Epic workspace is free.</span>;
  return saved ? <span role="status">Recovery decision saved.</span> : null;
}

// Once the server reports a newer gate state (queued, then resume_pending on a
// failed delivery), it drives status and buttons, not the finished request.
// Errors stay visible.
function useResetOnGateChange(gate: FactoryRecoveryGate, { isSuccess, reset }: { isSuccess: boolean; reset: () => void }) {
  const gateState = `${gate.resolution}|${gate.response ?? ''}`;
  const seenState = useRef(gateState);
  useEffect(() => {
    if (seenState.current === gateState) return;
    seenState.current = gateState;
    if (isSuccess) reset();
  }, [gateState, isSuccess, reset]);
}
