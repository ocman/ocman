import { Link } from 'react-router-dom';
import type { FactoryEpic } from '../lib/api';

/** Open epics as linked cards: goal, clamped brief, and required-work progress. */
export function FactoryEpicCards({ epics }: { epics: FactoryEpic[] }) {
	const open = epics.filter((epic) => epic.status === 'open');
	if (!open.length) return null;
	return <section className="factory-epic-cards" aria-label="Epics in progress">
		{open.map((epic) => {
			const { requiredSucceeded = 0, requiredTotal = 0 } = epic.progress ?? {};
			return <Link key={epic.id} className="factory-epic-card" to={`/factory/epics/${encodeURIComponent(epic.id)}`}>
				<strong>{epic.goal}</strong>
				{epic.brief && <p>{epic.brief}</p>}
				<span className="factory-epic-card-progress">
					<progress value={requiredSucceeded} max={requiredTotal || 1} aria-label={`${epic.goal} required issues done`} />
					<span>{requiredSucceeded}/{requiredTotal}</span>
				</span>
			</Link>;
		})}
	</section>;
}
