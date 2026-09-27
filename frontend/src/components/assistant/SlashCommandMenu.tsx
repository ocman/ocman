import type { SlashCommand } from '../../lib/api';

interface SlashCommandMenuProps {
  commands: SlashCommand[];
  activeIndex: number;
  menuRef: React.RefObject<HTMLDivElement | null>;
  listboxId: string;
  optionId: (index: number) => string;
  onSelect: (cmd: SlashCommand) => void;
  onHover: (index: number) => void;
}

export function SlashCommandMenu({ commands, activeIndex, menuRef, listboxId, optionId, onSelect, onHover }: SlashCommandMenuProps) {
  if (commands.length === 0) return null;

  return (
    <div className="oc-slash-menu" ref={menuRef} id={listboxId} role="listbox" aria-label="Slash commands">
      {commands.map((cmd, i) => (
        <div
          key={cmd.name}
          id={optionId(i)}
          role="option"
          aria-selected={i === activeIndex}
          className={`oc-slash-item${i === activeIndex ? ' active' : ''}`}
          onMouseDown={(e) => { e.preventDefault(); onSelect(cmd); }}
          onMouseEnter={() => onHover(i)}
        >
          <span className="oc-slash-name">/{cmd.name}</span>
          {cmd.description && <span className="oc-slash-desc">{cmd.description}</span>}
          {/* OpenCode reports command | skill | mcp; ocman's own commands carry no source. */}
          <span className="oc-slash-kind">{cmd.source || 'built-in'}</span>
        </div>
      ))}
    </div>
  );
}
