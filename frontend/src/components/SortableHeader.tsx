import type { UrlSort } from '../lib/useUrlSort';
import { Button } from './Control';
import './SortableHeader.css';

interface Props<K extends string> {
  column: K;
  sort: UrlSort<K>;
  children: string;
  /** Plain-language definition, shown on hover and announced with the header. */
  description?: string;
  numeric?: boolean;
}

/** Column header that sorts its table; the state is exposed through aria-sort. */
export function SortableHeader<K extends string>({ column, sort, children, description, numeric }: Props<K>) {
  const active = sort.key === column;
  const direction = active ? (sort.desc ? 'descending' : 'ascending') : 'none';
  return <th aria-sort={direction} className={numeric ? 'sortable-header-numeric' : undefined}>
    <Button type="button" variant="ghost" size="small" className="sortable-header" title={description}
      onClick={() => sort.toggle(column)} aria-description={description}>
      {children}
      <i className={`bi ${active ? (sort.desc ? 'bi-caret-down-fill' : 'bi-caret-up-fill') : 'bi-chevron-expand'}`} aria-hidden="true" />
    </Button>
  </th>;
}
