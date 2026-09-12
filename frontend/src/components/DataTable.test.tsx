// @vitest-environment jsdom

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { DataTable, DataTableGroup, DataTableRow } from './DataTable';

describe('DataTable', () => {
  it('keeps table props and feature classes', () => {
    render(<DataTable aria-label="Jobs" className="jobs"><tbody><tr><td>Build</td></tr></tbody></DataTable>);

    expect(screen.getByRole('table', { name: 'Jobs' })).toHaveClass('oc-data-table', 'jobs');
  });

  it('labels grouped rows', () => {
    render(<DataTableGroup label="Open" noun="issues" count={1}><DataTableRow primary="Fix it" secondary="issue-1" meta="Ready" /></DataTableGroup>);

    expect(screen.getByRole('region', { name: 'Open issues' })).toContainElement(screen.getByRole('listitem'));
    expect(screen.getByRole('listitem')).toHaveTextContent('Fix itissue-1Ready');
  });
});
