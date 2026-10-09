import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { Button, RouteButton } from './Control';
import { Popover } from './Popover';
import './FactoryIssueReference.css';

export function FactoryIssueReference({ issueID, children }: { issueID: string; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const [focusCard, setFocusCard] = useState(false);
  const [position, setPosition] = useState({ top: 0, left: 12 });
  const trigger = useRef<HTMLButtonElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const id = useId();
  const epicID = issueID.split('.')[0];
  const issues = useQuery({
    queryKey: ['factory-epics', epicID, 'issues'],
    queryFn: ({ signal }) => api.factoryIssues(epicID, signal),
    enabled: open,
    staleTime: 60_000,
  });
  const issue = issues.data?.find((item) => item.id === issueID);
  useEffect(() => () => clearTimeout(timer.current), []);
  function show(focus = false) {
    clearTimeout(timer.current);
    const anchor = trigger.current!.getBoundingClientRect();
    setPosition({ top: Math.max(12, Math.min(anchor.bottom + 4, window.innerHeight - 180)), left: Math.max(12, Math.min(anchor.left, window.innerWidth - 352)) });
    setFocusCard(focus);
    setOpen(true);
  }
  function leave() {
    timer.current = setTimeout(() => {
      if (!document.getElementById(id)?.contains(document.activeElement)) setOpen(false);
    }, 150);
  }
  return <>
    <Button ref={trigger} type="button" variant="link" size="compact" className="oc-factory-issue-reference"
      aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? id : undefined}
      onMouseEnter={() => show()} onMouseLeave={leave} onFocus={(event) => { if (!document.getElementById(id)?.contains(event.relatedTarget)) show(); }} onClick={() => show(true)}
      onBlur={(event) => { if (!document.getElementById(id)?.contains(event.relatedTarget)) setOpen(false); }}>{children}</Button>
    <Popover open={open} onClose={() => setOpen(false)} triggerRef={trigger} id={id} label={`Issue ${issueID}`} autoFocus={focusCard} style={position}>
      <div className="oc-factory-issue-preview" onMouseEnter={() => clearTimeout(timer.current)} onMouseLeave={leave}
        onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget) && event.relatedTarget !== trigger.current) setOpen(false); }}>
        <span>{issueID}</span>
        {issue ? <strong>{issue.title}</strong> : issues.isError ? <><span role="alert">Could not load issue.</span><Button size="compact" onClick={() => void issues.refetch()}>Retry</Button></> : issues.isPending ? <span role="status">Loading issue...</span> : <span>Issue unavailable.</span>}
        <RouteButton size="compact" to={`/factory/issues/${encodeURIComponent(issueID)}`} onClick={() => setOpen(false)}>Open issue</RouteButton>
      </div>
    </Popover>
  </>;
}
