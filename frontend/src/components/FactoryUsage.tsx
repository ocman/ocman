import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { fetchJSON } from '../lib/api';
import { DataTable } from './DataTable';

export interface Usage {
	tokens: { input: number; output: number; cacheRead: number; cacheWrite: number };
	cost: number;
	estCost: number;
}

export interface EpicUsage {
	total: Usage;
	phases: Record<string, Usage>;
	attempts: { attemptId: string; workId: string; stage: string; session: { platform: string; id: string }; usage: Usage | null }[];
	incomplete: boolean;
}

const phases = { plan: 'Plan', implement: 'Implement', verify: 'Verify', deliver: 'Deliver' };
const money = (value: number) => `$${value.toFixed(4)}`;

function UsageCells({ usage }: { usage: Usage | null }) {
	return usage ? <>{[usage.tokens.input, usage.tokens.output, usage.tokens.cacheRead, usage.tokens.cacheWrite].map((value, index) => <td key={index}>{value.toLocaleString()}</td>)}<td>{money(usage.cost)}</td><td>{money(usage.estCost)}</td></> : <td colSpan={6}>Usage unavailable</td>;
}

function UsageHeader() {
	return <thead><tr>{['Work', 'Input', 'Output', 'Cache read', 'Cache write', 'Billed', 'Estimated'].map((label) => <th key={label} scope="col">{label}</th>)}</tr></thead>;
}

export function FactoryUsage({ epicID, attemptID, compact = false }: { epicID: string; attemptID?: string; compact?: boolean }) {
	const query = useQuery({
		queryKey: ['factory-usage', epicID],
		queryFn: ({ signal }) => fetchJSON<EpicUsage>(`/api/factory/epics/${encodeURIComponent(epicID)}/usage`, signal),
		refetchInterval: 10_000,
	});
	if (query.isError) return <p role="alert">Could not load usage.</p>;
	if (!query.data) return <p role="status">Loading usage…</p>;
	const data = query.data;
	if (attemptID) {
		const usage = data.attempts.find((attempt) => attempt.attemptId === attemptID)?.usage;
		return usage ? <span>Attempt: {(usage.tokens.input + usage.tokens.output + usage.tokens.cacheRead + usage.tokens.cacheWrite).toLocaleString()} tokens · Billed {money(usage.cost)} · Estimated {money(usage.estCost)}</span> : <span>Attempt usage unavailable</span>;
	}
	return <section aria-label={`Usage for ${epicID}`}>
		<h3>Usage</h3>
		{data.incomplete && <p role="status">Incomplete totals. Some attempt sessions are unavailable.</p>}
		<p>Includes subagents. Estimated cost uses model token prices, including subscription sessions.</p>
		<DataTable framed aria-label={`Phase usage for ${epicID}`}><UsageHeader /><tbody>
			<tr><th scope="row">Total</th><UsageCells usage={data.total} /></tr>
			{Object.entries(phases).map(([key, label]) => <tr key={key}><th scope="row">{label}</th><UsageCells usage={data.phases[key]} /></tr>)}
		</tbody></DataTable>
		{!compact && <details><summary>Attempts</summary><DataTable framed aria-label="Attempt usage"><UsageHeader /><tbody>{data.attempts.map((attempt) => <tr key={attempt.attemptId}><th scope="row">{phases[attempt.stage as keyof typeof phases]} · {attempt.workId} · {attempt.attemptId}{attempt.session.id && <> · <Link to={`/session/${encodeURIComponent(attempt.session.id)}?platform=${encodeURIComponent(attempt.session.platform)}`}>Open session</Link></>}</th><UsageCells usage={attempt.usage} /></tr>)}</tbody></DataTable></details>}
	</section>;
}
