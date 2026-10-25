import { Link } from 'react-router-dom';
import { useState, type ReactNode } from 'react';
import { Button, ButtonGroup } from './Control';
import { DataTable } from './DataTable';
import { EmptyState } from './EmptyState';
import { pageURL, type PluginPage, type PageDocument, type PageValue, type PageCollection } from '../lib/pluginPages';
import { PluginPageValue } from './PluginPageValue';
import { PluginItemDrawer } from './PluginItemDrawer';
import './PluginPageContent.css';

function itemTitle(value: PageValue) {
  return typeof value === 'object' ? value.label : String(value);
}

export function PluginPageContent({ page, projectId, document, disabled, cursor, onCursor, emptyText }: {
  page: PluginPage; projectId: string; document: PageDocument; disabled: boolean; cursor: string; onCursor: (cursor: string) => void; emptyText?: string;
}) {
  const { collection, detail } = document;
  const [selected, setSelected] = useState<{ id: string; title: string } | null>(null);
  const linkedName = collection?.columns[0]?.display === 'summary';
  function itemURL(id: string, configurationProject?: string) {
    return `${pageURL(page)}&${new URLSearchParams(configurationProject ? { projectId: configurationProject } : { projectId, itemId: id })}`;
  }
  function itemAction(row: PageCollection['rows'][number], content?: ReactNode) {
    if (disabled) return content ?? 'Save or discard edits to open details.';
    if (collection?.detailPresentation === 'drawer' && !row.projectId) {
      const title = itemTitle(row.values[collection.columns[0].key]);
      return <Button variant="link" className={content ? 'plugin-item-open' : undefined} aria-label={content ? `View ${title}` : undefined}
        onClick={() => setSelected({ id: row.id, title })}>{content ?? 'View'}</Button>;
    }
    return <Link to={itemURL(row.id, row.projectId)} aria-label={`View ${row.id}`}>{content ?? 'View'}</Link>;
  }
  return <>
    {detail && <section aria-label={detail.title}><h2>{detail.title}</h2>
      <DataTable framed><tbody>{detail.properties.map((property, i) => <tr key={i}>
        <th scope="row">{property.label}</th><td className="plugin-detail-value"><PluginPageValue value={property.value} display={property.display} /></td>
      </tr>)}</tbody></DataTable>
    </section>}
    {collection && <section aria-label={collection.title}><h2>{collection.title}</h2>
      {collection.rows.length === 0 ? <EmptyState>{emptyText ?? 'No items.'}</EmptyState> : <DataTable framed className={linkedName ? 'plugin-summary-table' : ''}>
        <thead><tr>{collection.columns.map((col) => <th scope="col" key={col.key}>{col.label}</th>)}{!linkedName && <th scope="col">Details</th>}</tr></thead>
        <tbody>{collection.rows.map((row) => <tr key={row.id}>
          {collection.columns.map((col, i) => <td key={col.key}>{linkedName && i === 0
            ? itemAction(row, <PluginPageValue value={row.values[col.key]} display={col.display} />)
            : <PluginPageValue value={row.values[col.key]} display={col.display} />}</td>)}
          {!linkedName && <td>{itemAction(row)}</td>}
        </tr>)}</tbody>
      </DataTable>}
      {(cursor || collection.nextCursor) && <ButtonGroup label="List pagination">
        {cursor && <Button disabled={disabled} onClick={() => onCursor('')}>First page</Button>}
        {collection.nextCursor && <Button disabled={disabled} onClick={() => onCursor(collection.nextCursor!)}>Next page</Button>}
      </ButtonGroup>}
    </section>}
    {selected && <PluginItemDrawer key={selected.id} page={page} projectId={projectId} itemId={selected.id} title={selected.title} onClose={() => setSelected(null)} />}
  </>;
}
