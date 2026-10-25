import { Link } from 'react-router-dom';
import { Button, ButtonGroup } from './Control';
import { DataTable } from './DataTable';
import { EmptyState } from './EmptyState';
import { pageURL, type PluginPage, type PageDocument, type PageDisplay } from '../lib/pluginPages';

function displayValue(value: string | number | boolean, display: PageDisplay) {
  if (display === 'boolean') return value ? 'Yes' : 'No';
  if (display === 'status') return String(value).replaceAll('_', ' ');
  return String(value);
}

export function PluginPageContent({ page, projectId, document, disabled, cursor, onCursor }: {
  page: PluginPage; projectId: string; document: PageDocument; disabled: boolean; cursor: string; onCursor: (cursor: string) => void;
}) {
  const { collection, detail } = document;
  function itemURL(id: string, configurationProject?: string) {
    return `${pageURL(page)}&${new URLSearchParams(configurationProject ? { projectId: configurationProject } : { projectId, itemId: id })}`;
  }
  return <>
    {detail && <section aria-label={detail.title}><h2>{detail.title}</h2>
      <DataTable framed><tbody>{detail.properties.map((property, i) => <tr key={i}>
        <th scope="row">{property.label}</th><td>{displayValue(property.value, property.display)}</td>
      </tr>)}</tbody></DataTable>
    </section>}
    {collection && <section aria-label={collection.title}><h2>{collection.title}</h2>
      {collection.rows.length === 0 ? <EmptyState>No items.</EmptyState> : <DataTable framed>
        <thead><tr>{collection.columns.map((col) => <th scope="col" key={col.key}>{col.label}</th>)}<th scope="col">Details</th></tr></thead>
        <tbody>{collection.rows.map((row) => <tr key={row.id}>
          {collection.columns.map((col) => <td key={col.key}>{displayValue(row.values[col.key], col.display)}</td>)}
          <td>{disabled ? 'Save or discard edits to open details.' : <Link to={itemURL(row.id, row.projectId)} aria-label={`View ${row.id}`}>View</Link>}</td>
        </tr>)}</tbody>
      </DataTable>}
      {(cursor || collection.nextCursor) && <ButtonGroup label="List pagination">
        {cursor && <Button disabled={disabled} onClick={() => onCursor('')}>First page</Button>}
        {collection.nextCursor && <Button disabled={disabled} onClick={() => onCursor(collection.nextCursor!)}>Next page</Button>}
      </ButtonGroup>}
    </section>}
  </>;
}
