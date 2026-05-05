// Re-export so existing imports of `../pages/SessionDetail` continue
// to resolve via the directory's index after the file move. New
// imports should target `'./pages/session-detail'` directly.
export { SessionDetail } from './SessionDetail';
