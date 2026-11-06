import { useEffect, useState } from 'react';
import { bootstrapIconName, pluginIconURL, type PluginIconDescriptor } from '../lib/pluginIcons';
import './PluginNavIcon.css';

export function PluginNavIcon({ ownerId, pluginId, icon }: { ownerId: string; pluginId: string; icon?: PluginIconDescriptor }) {
  const url = pluginIconURL(ownerId, pluginId, icon);
  return url ? <AssetIcon key={url} url={url} /> : <i className={`bi bi-${bootstrapIconName(icon)}`} aria-hidden="true" />;
}

function AssetIcon({ url }: { url: string }) {
  const [blobURL, setBlobURL] = useState<string>();
  const [ready, setReady] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    let objectURL: string | undefined;
    void (async () => {
      try {
        const response = await fetch(url, { signal: controller.signal, credentials: 'same-origin', redirect: 'error', cache: 'no-store' });
        if (!response.ok || response.headers.get('Content-Type') !== 'image/svg+xml') return;
        const data = await response.arrayBuffer();
        if (controller.signal.aborted || data.byteLength === 0 || data.byteLength > 65536) return;
        objectURL = URL.createObjectURL(new Blob([data], { type: 'image/svg+xml' }));
        setBlobURL(objectURL);
      } catch {
        // Keep the puzzle for offline owners, cancellation and failed assets.
      }
    })();
    return () => { controller.abort(); if (objectURL) URL.revokeObjectURL(objectURL); };
  }, [url]);
  return <i className="plugin-nav-icon" aria-hidden="true">
    {!ready && <b className="bi bi-puzzle" />}
    {blobURL && <img className="plugin-nav-icon-probe" src={blobURL} alt="" onLoad={() => setReady(true)} onError={() => setReady(false)} />}
    {ready && blobURL && <b className="plugin-nav-icon-mask" style={{ maskImage: `url("${blobURL}")`, WebkitMaskImage: `url("${blobURL}")` }} />}
  </i>;
}
