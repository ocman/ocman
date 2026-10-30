import { useContext, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { buildSurface, parseA2UIResult, resolveValue } from '../../lib/a2ui';
import type { A2UIAction, A2UIResult as Result } from '../../lib/a2ui';
import { A2UIActionContext } from '../../lib/a2uiActionContext';
import { Button, CheckboxField, SubmitButton, TextField } from '../Control';
import { Modal } from '../Modal';
import { ModalFooter } from '../ModalFooter';
import { ModalHeader } from '../ModalHeader';
import { ResultBarChart, ResultTable } from './A2UIWidgets';
import styles from './A2UIResult.module.css';

export function A2UIToolResult({ toolName, argsText = '', result }: { toolName: string; argsText?: string; result: unknown }) {
  const context = useContext(A2UIActionContext);
  const ui = useMemo(() => parseA2UIResult(toolName, argsText, result), [toolName, argsText, result]);
  return ui ? <A2UIResult key={`${context.scope}:${JSON.stringify(ui)}`} result={ui} /> : null;
}

export function A2UIResult({ result }: { result: Result }) {
  const surface = useMemo(() => buildSurface(result), [result]);
  return surface ? <ResultBody result={result} surface={surface} /> : null;
}

function ResultBody({ result, surface }: { result: Result; surface: NonNullable<ReturnType<typeof buildSurface>> }) {
  const { send } = useContext(A2UIActionContext);
  const [data, setData] = useState(surface.data);
  const [pending, setPending] = useState<A2UIAction | null>(null);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState('');
  const [sent, setSent] = useState(false);
  const lock = useRef(false);
  function render(id: string): ReactNode {
    const c = surface.components.get(id)!; // graph validated before rendering
    switch (c.component) {
      case 'Text': {
        const text = String(resolveValue(c.text, data) ?? '');
        if (c.variant === 'h2') return <h2>{text}</h2>;
        if (c.variant === 'h3') return <h3>{text}</h3>;
        return <p className={c.variant === 'caption' ? styles.caption : undefined}>{text}</p>;
      }
      case 'Column':
      case 'Row': return <div className={c.component === 'Row' ? styles.row : styles.column}>{c.children.map(child => <div key={child}>{render(child)}</div>)}</div>;
      case 'Card': return <div className={styles.card}>{render(c.child)}</div>;
      case 'Divider': return <hr className={styles.divider} />;
      case 'TextField': return <label className={styles.field}>
        <span>{c.label}</span>
        <TextField disabled={!send} value={String(resolveValue(c.value, data) ?? '')} maxLength={8000}
          onChange={event => setData({ ...data, [c.value.path.slice(1)]: event.target.value })} />
      </label>;
      case 'CheckBox': return <label className={styles.row}>
        <CheckboxField disabled={!send} checked={resolveValue(c.value, data) === true}
          onChange={event => setData({ ...data, [c.value.path.slice(1)]: event.target.checked })} />
        <span>{c.label}</span>
      </label>;
      case 'Table': return <ResultTable columns={c.columns} rows={c.rows} />;
      case 'BarChart': return <ResultBarChart label={c.label} data={c.data} />;
      case 'Button': return <Button type="button" disabled={!send} title={!send ? 'This conversation is read-only or disconnected' : 'Review before sending to the agent'} onClick={() => {
        const context = Object.fromEntries(Object.entries(c.action.event.context ?? {}).map(([key, value]) => [key, resolveValue(value, data) ?? null]));
        setSent(false);
        setError('');
        setPending({ action: { name: c.action.event.name, surfaceId: surface.id, sourceComponentId: id, timestamp: new Date().toISOString(), context } });
      }}>{render(c.child)}</Button>;
    }
  }
  return <section className={styles.result} aria-label={result.title}>
    <header><h3>{result.title}</h3><span className={styles.caption}>Agent-generated result</span></header>
    <p>{result.summary}</p>
    {render('root')}
    {!send && <p className={styles.caption}>Read-only. Connect to the owning session to respond.</p>}
    {sent && <p role="status">Response sent to the agent.</p>}
    {pending && <Modal label="Review response to agent" canClose={!sending} onClose={() => setPending(null)}>
      <ModalHeader title="Review response to agent" closeLabel="Close response review" canClose={!sending} onClose={() => setPending(null)} />
      <p>This sends your response to this conversation. It does not directly execute the requested action.</p>
      <pre className={styles.preview}>{JSON.stringify(pending, null, 2)}</pre>
      {error && <p role="alert">{error}</p>}
      <ModalFooter label="Response actions">
        <Button type="button" data-autofocus disabled={sending} onClick={() => setPending(null)}>Cancel</Button>
        <SubmitButton type="button" variant="accent" disabled={!send} pending={sending} pendingLabel="Sending…" onClick={async () => {
          if (!send || lock.current) return;
          lock.current = true;
          setSending(true);
          setError('');
          try {
            await send(pending);
            setPending(null);
            setSent(true);
          } catch (err) {
            setError(err instanceof Error ? err.message : 'Could not send response. Try again.');
          } finally {
            lock.current = false;
            setSending(false);
          }
        }}>Send response</SubmitButton>
      </ModalFooter>
    </Modal>}
  </section>;
}
