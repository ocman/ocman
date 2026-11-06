import { createPortal } from 'react-dom';
import { Link, useSearchParams } from 'react-router-dom';
import { Button } from './Control';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './Tabs';
import { lazyComponent } from './lazyComponent';
import { ResourceViewer } from './ResourceViewer';
import './ActivityNavigation.css';

const BackgroundTasks = lazyComponent(() => import('../pages/BackgroundTasks').then((m) => m.BackgroundTasks));

export function ActivityNavigation({ onNavigate, triggerOnly = false }: { onNavigate?: () => void; triggerOnly?: boolean }) {
  const [params, setParams] = useSearchParams();
  const tab = params.get('activity');
  const open = tab === 'tasks' || tab === 'resources';
  const changeTab = (value?: string) => {
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (value) next.set('activity', value);
      else next.delete('activity');
      return next;
    });
  };
  if (triggerOnly) {
    const destination = new URLSearchParams(params);
    destination.set('activity', 'tasks');
    return <Link to={{ search: destination.toString() }} replace onClick={onNavigate}>
      <i className="bi bi-activity" aria-hidden="true" /><span>Activity</span><i className="bi bi-chevron-right" aria-hidden="true" />
    </Link>;
  }
  return <>
    <Button variant="ghost" className={`main-nav-usage${open ? ' active' : ''}`}
      aria-label="Activity" title="Activity" aria-haspopup="dialog" aria-expanded={open}
      onClick={() => { changeTab('tasks'); onNavigate?.(); }}>
      <i className="bi bi-activity" aria-hidden="true" /><span>Activity</span>
    </Button>
    {open && createPortal(<Modal label="Activity" onClose={() => changeTab()}>
      <div className="activity-modal">
        <ModalHeader title="Activity" onClose={() => changeTab()} closeLabel="Close activity" />
        <Tabs value={tab} onValueChange={changeTab}>
          <TabsList aria-label="Activity views">
            <TabsTrigger value="tasks">Background tasks</TabsTrigger>
            <TabsTrigger value="resources">Resources</TabsTrigger>
          </TabsList>
          <TabsContent value="tasks"><BackgroundTasks /></TabsContent>
          <TabsContent value="resources"><ResourceViewer /></TabsContent>
        </Tabs>
      </div>
    </Modal>, document.body)}
  </>;
}
