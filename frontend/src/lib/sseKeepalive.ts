/**
 * The server sends `ocman.keepalive` every 15s on every browser stream. A
 * dropped connection can leave an EventSource open with no error, so
 * silence past this window means the stream is dead.
 */
export const KEEPALIVE_TIMEOUT_MS = 40_000;

/**
 * Calls onSilent when the stream goes a full window without a keepalive,
 * counting from now (a connect that hangs that long is dead too).
 * Returns stop; call it whenever the stream is closed or replaced.
 */
export function watchKeepalive(source: EventSource, onSilent: () => void): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const arm = () => {
    clearTimeout(timer);
    timer = setTimeout(onSilent, KEEPALIVE_TIMEOUT_MS);
  };
  arm();
  source.addEventListener('ocman.keepalive', arm);
  // A stopped stream is closed or replaced and dispatches nothing more.
  return () => clearTimeout(timer);
}
