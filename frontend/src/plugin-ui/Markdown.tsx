import { MarkdownContent } from '../components/assistant/MarkdownText';
import '../components/AssistantThread.css';

/** Shared host prose rendering, without session-specific Factory cards or previews. */
export function Markdown({ text }: { text: string }) {
  return <div className="oc-md"><MarkdownContent text={text} factoryCards={false} /></div>;
}
