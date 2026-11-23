import { useSearchParams } from 'react-router-dom';
import { EXPLORE_FILE_PARAM, isExploreFilePath } from '../lib/exploreLinks';
import { lazyComponent } from './lazyComponent';

const ExploreModal = lazyComponent(() => import('./ExploreModal').then(m => m.ExploreModal), 'Loading files…');

export function LinkedFileExplorer({ dir, remoteId }: { dir: string; remoteId: string }) {
  const [params, setParams] = useSearchParams();
  const path = params.get(EXPLORE_FILE_PARAM);
  if (!path || !isExploreFilePath(path)) return null;
  function close() {
    setParams(current => {
      const next = new URLSearchParams(current);
      next.delete(EXPLORE_FILE_PARAM);
      return next;
    }, { replace: true });
  }
  return <ExploreModal key={JSON.stringify([dir, remoteId, path])} dir={dir} remoteId={remoteId} initialFile={path} onClose={close} />;
}
