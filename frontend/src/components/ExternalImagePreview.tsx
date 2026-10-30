import { useContext, useState } from 'react';
import { ExternalImageConsentContext } from '../lib/externalImageContext';
import { Button } from './Control';
import styles from './ExternalImagePreview.module.css';

export function ExternalImagePreview({ url, alt }: { url: URL; alt: string }) {
  const [allowed, setAllowed] = useState(false);
  const enabled = useContext(ExternalImageConsentContext);
  return <span className={styles.preview}>
    {alt && <span>{alt}</span>}
    {!enabled ? <span className={styles.warning}>External images are blocked in shared views.</span> : <>
      <span className={styles.warning}>Loading contacts {url.host} with this URL and may disclose your IP address or site cookies.</span>
      <code className={styles.url}>{url.href}</code>
      <Button type="button" size="small" onClick={() => setAllowed(!allowed)}>
        {allowed ? 'Stop loading external image' : `Load external image from ${url.host}`}
      </Button>
      {allowed && <iframe src={`/api/previews/external-image?url=${encodeURIComponent(url.href)}`}
        title={`External image: ${alt || 'Image'}`} sandbox="" referrerPolicy="no-referrer" className={styles.frame} />}
    </>}
  </span>;
}
