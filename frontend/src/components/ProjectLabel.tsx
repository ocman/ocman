import type { ComponentProps } from 'react';
import { shortPath } from '../lib/format';

type Props = Omit<ComponentProps<'span'>, 'children' | 'title'> & {
  path?: string;
  fallback?: string;
};

export function ProjectLabel({ path, fallback = '(unknown)', ...props }: Props) {
  return <span {...props} title={path || undefined}>{path ? shortPath(path) : fallback}</span>;
}
