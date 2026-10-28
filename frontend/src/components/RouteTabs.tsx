import { NavLink, useLocation } from 'react-router-dom';
import './RouteTabs.css';

export interface RouteTab {
  /** Route path, relative to the current route unless it starts with `/`. */
  to: string;
  label: string;
  /** Match only this exact path, not its sub-routes. */
  end?: boolean;
}

/**
 * Route views styled as tabs. They stay links, so they keep keyboard,
 * new-tab and history behavior; the active one carries aria-current.
 * Switching views keeps the current search parameters.
 */
export function RouteTabs({ label, tabs, className = '' }: { label: string; tabs: RouteTab[]; className?: string }) {
  const { search } = useLocation();
  return <nav className={`nav-tabs ${className}`.trim()} aria-label={label}>
    {tabs.map((tab) => <NavLink key={tab.to} className="nav-tab" end={tab.end} to={{ pathname: tab.to, search }}>{tab.label}</NavLink>)}
  </nav>;
}
