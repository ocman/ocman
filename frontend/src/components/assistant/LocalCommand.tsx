import { MarkdownText } from './MarkdownText';
import type { parseLocalCommand } from '../../lib/localCommand';

export function LocalCommand({ entry }: { entry: NonNullable<ReturnType<typeof parseLocalCommand>> }) {
  const command = entry.kind === 'command';
  const error = entry.kind === 'stderr';
  return (
    <div className={`oc-local-command${error ? ' oc-local-command-error' : ''}`} role="group" aria-label={command ? 'Local command' : error ? 'Command error' : 'Command output'}>
      <i className={`bi ${command ? 'bi-terminal' : error ? 'bi-exclamation-circle' : 'bi-chevron-right'}`} aria-hidden="true" />
      {command ? <code>{entry.text}</code> : <div className="oc-md"><MarkdownText text={entry.text} /></div>}
    </div>
  );
}
