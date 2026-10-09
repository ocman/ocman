import { useState } from 'react';
import { MarkdownContent } from './assistant/MarkdownText';

export function FactoryActionText({ text }: { text: string }) {
  const [expanded, setExpanded] = useState(false);
  return <div
    className="oc-md"
    role="button"
    tabIndex={0}
    aria-label={expanded ? 'Collapse action text' : 'Expand action text'}
    aria-expanded={expanded}
    style={{ cursor: 'pointer', ...(expanded ? {} : { display: '-webkit-box', WebkitBoxOrient: 'vertical', WebkitLineClamp: 7, overflow: 'hidden' }) }}
    onClick={(event) => { if (!(event.target as HTMLElement).closest('a, button')) setExpanded(!expanded); }}
    onKeyDown={(event) => {
      if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) {
        event.preventDefault();
        setExpanded(!expanded);
      }
    }}
  ><MarkdownContent text={text} factoryCards={false} /></div>;
}
