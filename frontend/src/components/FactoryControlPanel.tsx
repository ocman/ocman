import { useNavigate } from 'react-router-dom';
import { Button } from './Control';
import { useFactoryControl, useOpenFactoryControl } from '../lib/queries';

/** Entry to the Epic's control session: the one conversation where the user steers Factory. */
export function FactoryControlPanel({ epicID, closed }: { epicID: string; closed: boolean }) {
	const control = useFactoryControl(epicID);
	const open = useOpenFactoryControl(epicID);
	const navigate = useNavigate();
	const go = (sessionId: string) => navigate(`/session/${encodeURIComponent(sessionId)}?factoryEpic=${encodeURIComponent(epicID)}`);
	if (closed && !control.data) return null;
	return <section className="factory-epic-gate" aria-label="Control session">
		<h3>Control session</h3>
		<p>Steer this epic from one conversation. Factory posts every decision it needs there, and each change it proposes waits for your Allow.</p>
		{/* An open Epic always goes through the server, which replaces a session that no longer exists. */}
		{closed
			? control.data && <Button type="button" onClick={() => go(control.data!.sessionId)}>Open control session</Button>
			: <Button type="button" variant="accent" aria-busy={open.isPending} disabled={open.isPending || control.isLoading} onClick={() => open.mutate(undefined, { onSuccess: (session) => go(session.sessionId) })}>{open.isPending ? (control.data ? 'Opening…' : 'Starting…') : control.data ? 'Open control session' : 'Start control session'}</Button>}
		{(open.isError || control.isError) && <p role="alert">{(open.error ?? control.error) instanceof Error ? (open.error ?? control.error)!.message : 'Could not reach the control session.'}</p>}
	</section>;
}
