import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { api, type SlashCommand } from '../../lib/api';
import { BUILTIN_COMMANDS } from '../../lib/commands/builtinCommands';
import { fuzzyScore } from '../../lib/format';

export interface SlashMenuVisibility {
  hasModels: boolean;
  hasAgents: boolean;
  activeAgent: string | undefined;
  hasVariants: boolean;
}

export function reloadSlashCommands(sessionId: string, platform: string) {
  window.dispatchEvent(new CustomEvent('oc-slash-commands-reload', { detail: { sessionId, platform } }));
}

/**
 * The `/` autocomplete menu: fetches the session's command catalog
 * (built-ins merged with what the platform reports), tracks the open/
 * filter/highlight state driven by the textarea, and hides commands
 * whose feature isn't available on this session.
 */
export function useSlashMenu(sessionId: string | undefined, vis: SlashMenuVisibility, provided?: SlashCommand[], platform?: string) {
  const [fetched, setFetched] = useState<SlashCommand[]>([]);
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState('');
  const [index, setIndex] = useState(0);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!sessionId) return;
    let generation = 0;
    const load = () => {
      const current = ++generation;
      api.commands(sessionId, undefined, platform).then((cmds) => {
        if (current === generation) setFetched(cmds || []);
      }).catch(() => {
        if (current === generation) setFetched([]);
      });
    };
    const reload = (event: Event) => {
      const target = (event as CustomEvent<{ sessionId: string; platform?: string }>).detail;
      if (target?.sessionId === sessionId && target.platform === platform) load();
    };
    load();
    window.addEventListener('oc-slash-commands-reload', reload);
    return () => {
      generation++;
      window.removeEventListener('oc-slash-commands-reload', reload);
    };
  }, [sessionId, platform]);

  // Platform commands come from the session's catalog, or from the caller
  // when there is no session yet; built-ins fill the rest.
  const platformCommands = provided ?? fetched;
  const commands = [
    ...BUILTIN_COMMANDS.filter((b) => !platformCommands.some((f) => f.name === b.name)),
    ...platformCommands,
  ];

  const hasSkills = commands.some((c) => c.source === 'skill');
  const q = filter.toLowerCase();
  const filtered = commands
    .filter((cmd) => {
      if (cmd.name === 'model' && !vis.hasModels) return false;
      if ((cmd.name === 'agent' || cmd.name === 'agents') && !vis.hasAgents && !vis.activeAgent) return false;
      if (cmd.name === 'skills' && !hasSkills) return false;
      // Mirror OpenCode: /variants is hidden when the model exposes no variants.
      return !(cmd.name === 'variants' && !vis.hasVariants);
    })
    .map((cmd) => {
      const byName = fuzzyScore(filter, cmd.name);
      // Name matches (+1) always outrank a plain substring hit in the description.
      const score = byName >= 0 ? byName + 1 : q && cmd.description?.toLowerCase().includes(q) ? 0 : -1;
      return { cmd, score };
    })
    .filter((r) => r.score >= 0)
    .sort((a, b) => b.score - a.score) // stable: ties keep catalog order
    .map((r) => r.cmd);

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
    // Ranking reorders the list on every keystroke; keep the best match highlighted.
    setIndex(0);
  }, []);

  const moveIndex = useCallback((delta: 1 | -1) => {
    const n = Math.max(filtered.length, 1);
    setIndex((i) => (i + delta + n) % n);
  }, [filtered.length]);

  // ARIA wiring for the textarea: focus stays in it and the highlighted
  // option is announced via aria-activedescendant. The textarea keeps its
  // native textbox role (valid host for both attributes) rather than
  // becoming a combobox, which suits a multiline message field.
  const listboxId = useId();
  const expanded = open && filtered.length > 0;
  const optionId = (i: number) => `${listboxId}-opt-${i}`;
  const inputAria = {
    'aria-autocomplete': 'list',
    'aria-controls': expanded ? listboxId : undefined,
    'aria-activedescendant': expanded ? optionId(index) : undefined,
  } as const;

  return { commands, open, filtered, index, setIndex, moveIndex, menuRef, close, syncToInput, listboxId, optionId, inputAria };
}
