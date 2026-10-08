import type { ReactNode } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { usePageTitle } from '../lib/headerContext';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../components/Tabs';
import styles from './ProjectShell.module.css';

export function ProjectShell({ view, children }: { view: 'sessions' | 'worktrees' | 'settings'; children: ReactNode }) {
  const { dir = '' } = useParams();
  const directory = decodeURIComponent(dir);
  const [params] = useSearchParams();
  const remoteId = params.get('remoteId') || 'local';
  const navigate = useNavigate();
  usePageTitle(directory.split('/').pop() || 'Project');
  return <div className={styles.shell}>
    <Tabs value={view} onValueChange={(next) => {
      const query = new URLSearchParams(params);
      query.set('remoteId', remoteId);
      navigate({ pathname: `/project/${encodeURIComponent(directory)}${next === 'sessions' ? '' : `/${next}`}`, search: `?${query}` });
    }}>
      <TabsList aria-label="Project views" className={styles.tabs}>
        <TabsTrigger value="sessions">Sessions</TabsTrigger>
        <TabsTrigger value="worktrees">Worktrees</TabsTrigger>
        <TabsTrigger value="settings">Settings</TabsTrigger>
      </TabsList>
      <TabsContent value={view}>{children}</TabsContent>
    </Tabs>
  </div>;
}
