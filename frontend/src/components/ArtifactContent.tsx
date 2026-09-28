import { formatBytes, previewKind, type ArtifactItem } from '../lib/artifactsApi';
import { ArtifactPreview } from './ArtifactPreview';
import { AnchorButton, ButtonGroup } from './Control';
import { MarkdownContent } from './assistant/MarkdownText';
import './Artifacts.css';

interface Props {
  description?: string;
  links: ArtifactItem[];
  files: ArtifactItem[];
  /** Download href for a file; the detail page asks the server for an attachment. */
  downloadHref?: (file: ArtifactItem) => string | undefined;
}

/** Description, links and file previews of one artifact; shared by the detail page and the public share view. */
export function ArtifactContent({ description, links, files, downloadHref = (f) => f.url }: Props) {
  return <>
    {description && <section className="artifact-description oc-md"><MarkdownContent text={description} /></section>}
    {links.length > 0 && (
      <section aria-label="Links" className="artifact-section"><h3>Links</h3><ul className="artifact-links">
        {links.map((l, i) => <li key={i}><a href={l.url} target="_blank" rel="noopener noreferrer">{l.label || l.url}</a></li>)}
      </ul></section>
    )}
    {files.length > 0 && (
      <section aria-label="Files" className="artifact-section"><h3>Files</h3>
        {files.map((f, i) => {
          const download = f.url && downloadHref(f);
          return <article key={i} className="artifact-file" data-testid="artifact-file">
            <header>
              <strong>{f.name}</strong>
              <span className="artifact-muted">{f.mime} · {formatBytes(f.size ?? 0)}</span>
              {f.url && <ButtonGroup label={`Actions for ${f.name}`}>
                {previewKind(f) === 'none' && <AnchorButton size="small" href={f.url} target="_blank" rel="noopener noreferrer">Open</AnchorButton>}
                {download && <AnchorButton size="small" href={download} download={f.name} aria-label={`Download ${f.name}`}><i className="bi bi-download" aria-hidden="true" />Download</AnchorButton>}
              </ButtonGroup>}
            </header>
            {f.url && <ArtifactPreview item={f} />}
          </article>;
        })}
      </section>
    )}
  </>;
}
