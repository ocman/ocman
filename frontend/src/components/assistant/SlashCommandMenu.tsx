import type { SlashCommand } from '../../lib/api';

interface SlashCommandMenuProps {
  commands: SlashCommand[];
  activeIndex: number;
  menuRef: React.RefObject<HTMLDivElement | null>;
  onSelect: (cmd: SlashCommand) => void;
  onHover: (index: number) => void;
}

export function SlashCommandMenu({ commands, activeIndex, menuRef, onSelect, onHover }: SlashCommandMenuProps) {
  if (commands.length === 0) return null;

  return (
    <div className="oc-slash-menu" ref={menuRef}>
      {commands.map((cmd, i) => (
        <div
          key={cmd.name}
          className={`oc-slash-item${i === activeIndex ? ' active' : ''}`}
          onMouseDown={(e) => { e.preventDefault(); onSelect(cmd); }}
          onMouseEnter={() => onHover(i)}
        >
          <span className="oc-slash-name">/{cmd.name}</span>
          {cmd.description && <span className="oc-slash-desc">{cmd.description}</span>}
        </div>
      ))}
    </div>
  );
}
