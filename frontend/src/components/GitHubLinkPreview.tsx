import { useState, useEffect } from 'react';
import type { FC } from 'react';
import { extractGitHubUrls, cachedGitHubPreview } from '../lib/githubPreview';
import type { GitHubPreviewData } from '../lib/githubPreview';
import './GitHubLinkPreview.css';

function formatRelativeDate(iso: string): string {
  if (!iso) return '';
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diff / 60_000);
  if (mins < 2) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  const months = Math.floor(days / 30);
  return `${months}mo ago`;
}

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
            <span className="gh-preview__date">{formatRelativeDate(data.updatedAt)}</span>
          </>
        )}
      </span>
    </span>
    <span className="gh-preview__external" aria-hidden="true">
      <i className="bi bi-arrow-up-right" />
    </span>
  </a>
);

/**
 * Fetches and renders a single preview card for one URL.
 * Returns null while loading or if the fetch failed / URL unrecognised.
 */
const SinglePreview: FC<{ url: string }> = ({ url }) => {
  const [data, setData] = useState<GitHubPreviewData | null>(null);

  useEffect(() => {
    let cancelled = false;
    cachedGitHubPreview(url).then((d) => {
      if (!cancelled) setData(d);
    });
    return () => { cancelled = true; };
  }, [url]);

  if (!data) return null;
  return <PreviewCard data={data} />;
};

/**
 * Scans `text` for previewable GitHub URLs and renders a card for each one
 * below the text block. Renders nothing when there are no matching URLs.
 */
export const LinkPreviewStrip: FC<{ text: string }> = ({ text }) => {
  const urls = extractGitHubUrls(text);
  if (urls.length === 0) return null;
  return (
    <div className="gh-preview-strip" data-testid="gh-preview-strip">
      {urls.map((url) => (
        <SinglePreview key={url} url={url} />
      ))}
    </div>
  );
};
