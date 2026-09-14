import { useCallback, useEffect, useRef, useState } from 'react';
import { api, type SlashCommand } from '../../lib/api';
import { BUILTIN_COMMANDS } from '../../lib/commands/builtinCommands';

export interface SlashMenuVisibility {
  hasModels: boolean;
  hasAgents: boolean;
  activeAgent: string | undefined;
  hasVariants: boolean;
}

/**
 * The `/` autocomplete menu: fetches the session's command catalog
 * (built-ins merged with what the platform reports), tracks the open/
 * filter/highlight state driven by the textarea, and hides commands
 * whose feature isn't available on this session.
 */
export function useSlashMenu(sessionId: string | undefined, vis: SlashMenuVisibility) {
  const [commands, setCommands] = useState<SlashCommand[]>(BUILTIN_COMMANDS);
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState('');
  const [index, setIndex] = useState(0);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!sessionId) return;
    let cancelled = false;
    api.commands(sessionId).then((cmds) => {
      if (!cancelled) {
        const fetched = cmds || [];
        setCommands([
          ...BUILTIN_COMMANDS.filter((b) => !fetched.some((f) => f.name === b.name)),
          ...fetched,
        ]);
      }
    }).catch(() => {
      setCommands(BUILTIN_COMMANDS);
    });
    return () => { cancelled = true; };
  }, [sessionId]);

  const hasSkills = commands.some((c) => c.source === 'skill');
  const filtered = commands.filter((cmd) => {
    if (cmd.name === 'model' && !vis.hasModels) return false;
    if ((cmd.name === 'agent' || cmd.name === 'agents') && !vis.hasAgents && !vis.activeAgent) return false;
    if (cmd.name === 'skills' && !hasSkills) return false;
    // Mirror OpenCode: /variants is hidden when the model exposes no variants.
    if (cmd.name === 'variants' && !vis.hasVariants) return false;
    return cmd.name.toLowerCase().startsWith(filter.toLowerCase());
  });

  useEffect(() => {
    if (!open || !menuRef.current) return;
    const active = menuRef.current.querySelector('.oc-slash-item.active');
    if (active) active.scrollIntoView({ block: 'nearest' });
  }, [index, open]);

  const close = useCallback(() => {
    setOpen(false);
    setFilter('');
    setIndex(0);
  }, []);

  /** Sync the menu to the textarea value on every input event. */
  const syncToInput = useCallback((value: string) => {
    const show = value.startsWith('/') && !value.includes(' ') && !value.includes('\n');
    setOpen(show);
    setFilter(show ? value.slice(1) : '');
    if (!show) setIndex(0);
  }, []);

  const moveIndex = useCallback((delta: 1 | -1) => {
    const n = Math.max(filtered.length, 1);
    setIndex((i) => (i + delta + n) % n);
  }, [filtered.length]);

  return { commands, open, filtered, index, setIndex, moveIndex, menuRef, close, syncToInput };
}
