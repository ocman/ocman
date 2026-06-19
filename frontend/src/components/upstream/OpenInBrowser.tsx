interface OpenInBrowserProps {
  /** Destination URL (PR or Issue web page on the forge). */
  url: string;
  /** Host shown in the tooltip, e.g. "github.com". */
  host: string;
  /** Stable test id, e.g. `pr-row-42-open`. */
  testId: string;
}

/**
 * OpenInBrowser is the small external-link icon shown in a row's
 * summary. It opens the PR/Issue on the forge in a new tab WITHOUT
 * expanding the row — `stopPropagation` keeps the click off the
 * surrounding expand button, and it sits as a sibling of (not inside)
 * that button so it isn't a nested-interactive-element.
 */
export function OpenInBrowser({ url, host, testId }: OpenInBrowserProps) {
  return (
    <a
      className="oc-upstream-row-open"
      href={url}
      target="_blank"
      rel="noreferrer noopener"
      title={`Open on ${host}`}
      aria-label={`Open on ${host}`}
      data-testid={testId}
      onClick={(e) => e.stopPropagation()}
    >
      <ExternalLinkIcon />
    </a>
  );
}

function ExternalLinkIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M6 3.5H3.5v9h9V10M9.5 3.5H13v3.5M13 3.5 7.5 9"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.3"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
