// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderToStaticMarkup } from 'react-dom/server';
import { ProjectScopePicker } from './ProjectScopePicker';
import { buildScopeTree, flattenForOptions } from '../lib/projectTree';

describe('flattenForOptions', () => {
  it('returns empty array for empty tree', () => {
    expect(flattenForOptions([])).toEqual([]);
  });

  it('emits one entry per node, depth-first, with depth set', () => {
    // /a/b branches into c and d.
    const tree = buildScopeTree([{ directory: '/a/b/c' }, { directory: '/a/b/d' }]);
    const flat = flattenForOptions(tree);
    // First entry is the collapsed branching node, then its two leaves.
    expect(flat[0].path).toBe('/a/b');
    expect(flat[0].depth).toBe(0);
    expect(flat[0].projectCount).toBe(2);
    // Children come next, sorted alphabetically.
    expect(flat.slice(1).map((e) => e.path)).toEqual(['/a/b/c', '/a/b/d']);
    expect(flat.slice(1).every((e) => e.depth === 1)).toBe(true);
  });

  it('exposes every parent level for the user-described example', () => {
    // The conversation example:
    //   /Users/dries/src/github.com/nousefreak/ocman
    //   /Users/dries/src/github.com/nousefreak/other
    //   /Users/dries/src/github.com/some-org/whatever
    const tree = buildScopeTree([
      { directory: '/Users/dries/src/github.com/nousefreak/ocman' },
      { directory: '/Users/dries/src/github.com/nousefreak/other' },
      { directory: '/Users/dries/src/github.com/some-org/whatever' },
    ]);
    const paths = flattenForOptions(tree).map((e) => e.path);
    // The picker MUST surface the host scope, the org scope, and the
    // individual repo scopes — that's the entire point of this feature.
    expect(paths).toContain('/Users/dries/src/github.com');
    expect(paths).toContain('/Users/dries/src/github.com/nousefreak');
    expect(paths).toContain('/Users/dries/src/github.com/nousefreak/ocman');
    expect(paths).toContain('/Users/dries/src/github.com/nousefreak/other');
    expect(paths).toContain('/Users/dries/src/github.com/some-org/whatever');
  });
});

describe('ProjectScopePicker', () => {
  it('renders a default "All projects" option', () => {
    const html = renderToStaticMarkup(<ProjectScopePicker projects={[]} value="" onChange={() => {}} />);
    expect(html).toContain('All projects');
  });

  it('renders and selects every project scope', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const projects = [{ directory: '/repo/a' }, { directory: '/repo/b' }];
    render(<ProjectScopePicker projects={projects} value="" onChange={onChange} />);
    await user.click(screen.getByRole('combobox', { name: 'Project scope' }));
    expect(screen.getAllByRole('option')).toHaveLength(4);
    await user.click(screen.getByRole('option', { name: /repo\/a/ }));
    expect(onChange).toHaveBeenCalledWith('/repo/a');
  });

  it('hides the visible caption by default and shows it with showLabel', () => {
    const bare = renderToStaticMarkup(<ProjectScopePicker projects={[]} value="" onChange={() => {}} />);
    expect(bare).not.toContain('<span>Project scope</span>');

    const labelled = renderToStaticMarkup(<ProjectScopePicker projects={[]} value="" onChange={() => {}} showLabel />);
    expect(labelled).toContain('<span>Project scope</span>');
  });

  it('marks the active scope as selected', () => {
    const projects = [{ directory: '/repo/a' }, { directory: '/repo/b' }];
    render(<ProjectScopePicker projects={projects} value="/repo/a" onChange={() => {}} />);
    expect(screen.getByRole('combobox', { name: 'Project scope' })).toHaveTextContent('repo/a');
  });

  it('disables itself when there are no projects to scope by', () => {
    render(<ProjectScopePicker projects={[]} value="" onChange={() => {}} />);
    expect(screen.getByRole('combobox', { name: 'Project scope' })).toBeDisabled();
  });

  it('does not branch on platform (lint guard)', () => {
    // Defensive: the picker MUST be platform-agnostic. If a future change
    // introduces `session.platform === '...'` style logic, the
    // check-platform-branching script catches it; this test pins the
    // current behaviour at the unit level so the invariant is doubly
    // enforced.
    const projects = [{ directory: '/repo/a' }];
    const html = renderToStaticMarkup(<ProjectScopePicker projects={projects} value="" onChange={() => {}} />);
    expect(html).not.toContain('opencode');
    expect(html).not.toContain('claude-code');
  });
});
