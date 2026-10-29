import { useCallback, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import type { SessionDraftHandle, SessionDraftOptions } from '@ocman/plugin-ui';
import { seedConversationDraft } from '../lib/newConversationDrafts';
import { newSessionPath } from '../lib/newSessionPath';
import { projectsQuery } from '../lib/queries';
import { randomId } from '../lib/randomId';
import { remoteLog } from '../lib/remoteLog';
import { usePluginHost } from './context';

export const SESSION_DRAFT_LIMITS = { directory: 4096, title: 200, prompt: 65_536 };
// Line breaks and direction overrides would make the text look like something else.
// eslint-disable-next-line no-control-regex
const CONTROL = /[\u0000-\u001f\u007f-\u009f\u2028\u2029\u202a-\u202e\u2066-\u2069]/;

function optional(value: unknown, name: string, limit: number) {
  if (value === undefined) return '';
  if (typeof value !== 'string') throw new Error(`The draft ${name} must be text.`);
  if (value.length > limit) throw new Error(`The draft ${name} is longer than ${limit.toLocaleString('en-US')} characters.`);
  return value;
}

/** Plugin options are untrusted: check every field before anything is stored. */
export function validateSessionDraft(options: SessionDraftOptions) {
  if (!options || typeof options !== 'object') throw new Error('Session draft options are missing.');
  const directory = optional(options.directory, 'directory', SESSION_DRAFT_LIMITS.directory);
  if (!directory || CONTROL.test(directory)) throw new Error('The draft needs a project directory.');
  const title = optional(options.title, 'title', SESSION_DRAFT_LIMITS.title).trim();
  if (CONTROL.test(title)) throw new Error('The draft title must be one line of plain text.');
  const prompt = optional(options.prompt, 'prompt', SESSION_DRAFT_LIMITS.prompt);
  // The composer would run these as a command or shell input on send.
  if (/^\s*[/!]/.test(prompt)) throw new Error('The draft prompt cannot start with "/" or "!".');
  return { directory, title, prompt };
}

export function useSessionDraft(): SessionDraftHandle {
  const { ownerId } = usePluginHost();
  const navigate = useNavigate();
  const client = useQueryClient();
  const [state, setState] = useState<{ pending: boolean; error: string | null }>({ pending: false, error: null });
  const busy = useRef(false);
  const open = useCallback(async (options: SessionDraftOptions) => {
    if (busy.current) throw new Error('A session draft is already opening.');
    busy.current = true;
    setState({ pending: true, error: null });
    try {
      const { directory, title, prompt } = validateSessionDraft(options);
      const projects = await client.fetchQuery(projectsQuery)
        .catch(() => { throw new Error('Could not load projects. Check that ocman is reachable and retry.'); });
      if (!projects.some((p) => p.directory === directory && (p.remoteId || 'local') === ownerId)) {
        throw new Error(`${directory} is not a project on this machine.`);
      }
      const draft = { directory, remoteId: ownerId, title: title || undefined, draftId: randomId() };
      await seedConversationDraft(draft, prompt).catch((error: unknown) => {
        remoteLog.warn('Could not save a plugin session draft', error);
        throw new Error(`Could not save the draft: ${error instanceof Error ? error.message : String(error)}`);
      });
      setState({ pending: false, error: null });
      navigate(newSessionPath(draft));
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      setState({ pending: false, error: message });
      throw new Error(message);
    } finally {
      busy.current = false;
    }
  }, [client, navigate, ownerId]);
  return { open, pending: state.pending, error: state.error };
}
