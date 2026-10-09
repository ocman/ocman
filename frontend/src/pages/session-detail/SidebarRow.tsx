import type { HTMLAttributes } from 'react';

type Props = HTMLAttributes<HTMLDivElement> & {
  active: boolean;
  inGroup?: boolean;
  flat?: boolean;
  archiving?: boolean;
};

export function SidebarRow({ active, inGroup, flat, archiving, className = '', onKeyDown, ...props }: Props) {
  return <div {...props} role="button" tabIndex={0} aria-selected={active}
    className={`session-sidebar-item${active ? ' active' : ''}${archiving ? ' archiving' : ''}${inGroup ? ' in-group' : ''}${flat ? ' flat' : ''} ${className}`}
    onKeyDown={(event) => {
      if (event.target !== event.currentTarget) return;
      if (onKeyDown) onKeyDown(event);
      else if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        event.currentTarget.click();
      }
    }} />;
}
