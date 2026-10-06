import { Button, ButtonGroup } from './Control';
import type { ShareLinkLike, useShareLinks } from '../lib/useShareLinks';

export function ShareLinkActions<T extends ShareLinkLike>({ state, link, copyLabel = 'Copy' }: {
  state: ReturnType<typeof useShareLinks<T>>; link: T; copyLabel?: string;
}) {
  return <ButtonGroup label="Share link actions">
    <Button type="button" size="small" disabled={state.busy} onClick={() => void state.copy(link)} data-testid="share-copy-link">{state.copied === link.token ? 'Copied!' : copyLabel}</Button>
    <Button type="button" size="small" variant="danger" disabled={state.busy} onClick={() => void state.revoke(link)} data-testid="share-revoke-link">Revoke</Button>
  </ButtonGroup>;
}
