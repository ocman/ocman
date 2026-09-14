import type { ComponentProps, ReactNode } from 'react';
import './DataTable.css';

export function DataTable({ className = '', ...props }: ComponentProps<'table'>) {
  return <table className={`oc-data-table ${className}`.trim()} {...props} />;
}

export function DataTableGroupHeader({ className = '', ...props }: ComponentProps<'header'>) {
  return <header className={`oc-data-table-group-header ${className}`.trim()} {...props} />;
}

export function DataTableGroup({ label, noun, count, markerClassName = '', children }: { label: string; noun: string; count: number; markerClassName?: string; children: ReactNode }) {
  return <section className="oc-data-table-group" aria-label={`${label} ${noun}`}>
    <DataTableGroupHeader><span className={`oc-data-table-marker ${markerClassName}`.trim()} title={label} aria-hidden="true" /><span className="oc-data-table-group-title">{label}</span><span className="oc-data-table-group-count">{count}</span></DataTableGroupHeader>
    <div role="list">{children}</div>
  </section>;
}

export function DataTableRow({ primary, secondary, meta, className = '' }: { primary: ReactNode; secondary?: ReactNode; meta?: ReactNode; className?: string }) {
  return <div className={`oc-data-table-row ${className}`.trim()} role="listitem">
    <div className="oc-data-table-row-main">{primary}{secondary && <div className="oc-data-table-row-subline">{secondary}</div>}</div>
    {meta && <div className="oc-data-table-row-meta">{meta}</div>}
  </div>;
}
