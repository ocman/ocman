import { useState, useEffect, useRef, useCallback } from 'react';
import type { FC } from 'react';
import {
  extractGitHubUrls,
  cachedGitHubPreview,
  refreshGitHubPreview,
  extractForgejoUrls,
  cachedForgejoPreview,
  refreshForgejoPreview,
  loadForgejoHosts,
} from '../lib/githubPreview';
import type { GitHubPreviewData } from '../lib/githubPreview';
import { RelativeTime } from './RelativeTime';
import './GitHubLinkPreview.css';

const PreviewCard: FC<{ data: GitHubPreviewData }> = ({ data }) => (
  <a
    className={`gh-preview gh-preview--${data.stateClass}`}
    href={data.url}
    target="_blank"
    rel="noopener noreferrer"
    data-testid="gh-preview-card"
  >
    <span className="gh-preview__icon">
      <i className={`bi ${data.stateIcon}`} aria-hidden="true" />
    </span>
    <span className="gh-preview__body">
      <span className="gh-preview__title">{data.title}</span>
      <span className="gh-preview__meta">
        <span className={`gh-preview__state gh-preview__state--${data.stateClass}`}>
          {data.state}
        </span>
        <span className="gh-preview__sep">·</span>
        <span className="gh-preview__repo">{data.repo}</span>
        {data.shortSha && (
          <>
            <span className="gh-preview__sep">·</span>
            <span className="gh-preview__sha">{data.shortSha}</span>
          </>
        )}
        {data.author && (
          <>
            <span className="gh-preview__sep">·</span>
            {data.authorAvatar && (
              <img
                className="gh-preview__avatar"
                src={data.authorAvatar}
                alt=""
                width={14}
                height={14}
                aria-hidden="true"
              />
            )}
            <span className="gh-preview__author">{data.author}</span>
          </>
        )}
        {data.updatedAt && (
          <>
            <span className="gh-preview__sep">·</span>
            <span className="gh-preview__date"><RelativeTime iso={data.updatedAt} /></span>
          </>
        )}
      </span>
    </span>
    <span className="gh-preview__external" aria-hidden="true">
      <i className="bi bi-arrow-up-right" />
    </span>
  </a>
);

const REFRESH_INTERVAL_MS = 5_000;

/**
 * Fetches and renders a single preview card for one URL.
 * Silently refreshes every 5 s while the card is in the viewport,
 * and immediately on entering the viewport.
 */
const SinglePreview: FC<{
  url: string;
  load: (url: string) => Promise<GitHubPreviewData | null>;
  refresh: (url: string) => Promise<GitHubPreviewData | null>;
}> = ({ url, load, refresh }) => {
  const [data, setData] = useState<GitHubPreviewData | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const inViewRef = useRef(false);

  // Initial load from cache (or first fetch)
  useEffect(() => {
    let cancelled = false;
    load(url).then((d) => {
      if (!cancelled) setData(d);
    });
    return () => { cancelled = true; };
  }, [url, load]);

  const doRefresh = useCallback(() => {
    refresh(url).then((d) => {
      if (d) setData(d);
    });
  }, [url, refresh]);

  const startPolling = useCallback(() => {
    if (intervalRef.current !== null) return;
    intervalRef.current = setInterval(doRefresh, REFRESH_INTERVAL_MS);
  }, [doRefresh]);

  const stopPolling = useCallback(() => {
    if (intervalRef.current === null) return;
    clearInterval(intervalRef.current);
    intervalRef.current = null;
  }, []);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting && !inViewRef.current) {
          inViewRef.current = true;
          doRefresh();
          startPolling();
        } else if (!entry.isIntersecting && inViewRef.current) {
          inViewRef.current = false;
          stopPolling();
        }
      },
      { threshold: 0 },
    );

    observer.observe(el);
    return () => {
      observer.disconnect();
      stopPolling();
    };
  }, [doRefresh, startPolling, stopPolling]);

  return <div ref={ref}>{data && <PreviewCard data={data} />}</div>;
};

// Stable references so SinglePreview's effect deps don't churn.
const ghLoad = (url: string) => cachedGitHubPreview(url);
const ghRefresh = (url: string) => refreshGitHubPreview(url);

/**
 * Scans `text` for previewable forge URLs (GitHub + any configured Forgejo
 * hosts) and renders a card for each one below the text block. Renders
 * nothing when there are no matching URLs.
 */
export const LinkPreviewStrip: FC<{ text: string }> = ({ text }) => {
  const [forgejoHosts, setForgejoHosts] = useState<string[]>([]);

  useEffect(() => {
    let cancelled = false;
    loadForgejoHosts().then((hosts) => {
      if (!cancelled) setForgejoHosts(hosts);
    });
    return () => { cancelled = true; };
  }, []);

  const ghUrls = extractGitHubUrls(text);
  const fjUrls = extractForgejoUrls(text, forgejoHosts);

  const fjLoad = useCallback(
    (url: string) => cachedForgejoPreview(url, forgejoHosts),
    [forgejoHosts],
  );
  const fjRefresh = useCallback(
    (url: string) => refreshForgejoPreview(url, forgejoHosts),
    [forgejoHosts],
  );

  if (ghUrls.length === 0 && fjUrls.length === 0) return null;
  return (
    <div className="gh-preview-strip" data-testid="gh-preview-strip">
      {ghUrls.map((url) => (
        <SinglePreview key={url} url={url} load={ghLoad} refresh={ghRefresh} />
      ))}
      {fjUrls.map((url) => (
        <SinglePreview key={url} url={url} load={fjLoad} refresh={fjRefresh} />
      ))}
    </div>
  );
};
