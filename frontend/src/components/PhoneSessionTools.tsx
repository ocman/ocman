import { useState } from 'react';
import type { Session } from '../lib/api';
import { ExploreModal } from './ExploreModal';
import { IconButton } from './IconButton';
import { ProjectQuickSettings } from './ProjectQuickSettings';
import { HostBadge } from './HostBadge';
import { ProjectLabel } from './ProjectLabel';

export function PhoneSessionTools({ session }: { session?: Session }) {
  const [exploring, setExploring] = useState(false);
  return (
    <div className="phone-session-tools" role="group" aria-label="Session tools">
      {session && <>
        <ProjectQuickSettings directory={session.directory} remoteId={session.remoteId} className="phone-session-project" title={session.directory}>
          <HostBadge alwaysShow remoteId={session.remoteId} remoteName={session.remoteName} />
          <ProjectLabel path={session.directory} />
        </ProjectQuickSettings>
        <IconButton variant="ghost" icon="bi-folder2-open" label="Explore project files" onClick={() => setExploring(true)} />
        {exploring && <ExploreModal dir={session.directory} remoteId={session.remoteId || 'local'} onClose={() => setExploring(false)} />}
      </>}
      <div id="phone-session-tools-slot" />
    </div>
  );
}
