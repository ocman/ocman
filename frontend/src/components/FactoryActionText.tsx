import { useId, useState } from 'react';
import { MarkdownContent } from './assistant/MarkdownText';
import { Button } from './Control';

export function FactoryActionText({ text }: { text: string }) {
  const [expanded, setExpanded] = useState(false);
  const id = useId();
  return <div><div
    id={id}
    data-testid="factory-action-text"
    className="oc-md"
    style={{ cursor: 'pointer', ...(expanded ? {} : { display: '-webkit-box', WebkitBoxOrient: 'vertical', WebkitLineClamp: 7, overflow: 'hidden' }) }}
    onClick={(event) => { if (!(event.target as HTMLElement).closest('a, button')) setExpanded(!expanded); }}
  ><MarkdownContent text={text} factoryCards={false} /></div>
    <Button type="button" variant="ghost" aria-expanded={expanded} aria-controls={id} onClick={() => setExpanded(!expanded)}>{expanded ? 'Collapse action text' : 'Expand action text'}</Button>
  </div>;
}
