// Re-export so existing imports of `../pages/SessionDetail` continue
// to resolve via the directory's index after the file move. New
// imports should target `'./pages/session-detail'` directly.
//
// The default export reads :id from the URL via useParams() and
// passes it as a prop to the inner component. This indirection
// matters: without it, the inner component subscribes to the
// react-router param via context, and we have observed cases under
// sustained SSE activity where the URL changes but the inner
// component never re-renders with the new id (sidebar item stays
// "active" on the previously-viewed session, content stays stuck).
// Threading the value through as a prop guarantees the inner
// re-renders whenever id changes — function-component identity
// equality only short-circuits when ALL inputs are equal.
//
// We deliberately do NOT remount the inner component (no key={id}).
// SessionDetail owns the Recent-sessions sidebar and that should
// keep refreshing in the background across navigations rather than
// rebuilding from scratch.
//
// Tests for SessionDetail's internals import the inner component
// directly via `./SessionDetail` and pass `id` as a prop.
import { useParams } from 'react-router-dom';
import { SessionDetail as SessionDetailInner } from './SessionDetail';

export function SessionDetail() {
  const { id } = useParams<{ id: string }>();
  return <SessionDetailInner id={id} />;
}
