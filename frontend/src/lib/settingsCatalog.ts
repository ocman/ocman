/**
 * Every setting's title, description and example live here, so the settings
 * page can be searched without mounting every section, and a row cannot ship
 * without explaining itself. <SettingRow setting="…"> reads its copy from
 * this catalog; components only override the description for live state
 * (e.g. "blocked by your browser").
 */

export type SettingsGroupId =
  | 'notifications' | 'sessions' | 'remotes' | 'plugins' | 'auto-approve' | 'sharing'
  | 'webhooks' | 'templates' | 'link-previews' | 'maintenance' | 'behaviour' | 'app' | 'account'
  /** Per-project settings page; not part of the global Settings search. */
  | 'project';

export type SettingEntry = {
  group: SettingsGroupId;
  title: string;
  description: string;
  example: string;
  /** Extra search terms that appear in neither title nor description. */
  keywords?: string;
  /** Link previews tab the row lives on. */
  tab?: 'accounts' | 'apps' | 'rules';
  /**
   * When the row is shown only under some condition, the condition in a
   * sentence. Search shows it, and a jump that finds no row explains it.
   */
  requires?: string;
};

export const SETTINGS = {
  'reviewer-endpoint': {
    group: 'auto-approve',
    title: 'Reviewer endpoint',
    description: 'Use the existing OpenCode reviewer, or send permission requests directly to your own compatible API. You manage hosting and billing. Keys are stored on this ocman instance and never returned to the browser. Changing the URL clears the saved key unless you enter a replacement.',
    example: 'Choose OpenAI-compatible with http://127.0.0.1:8080/v1/chat/completions, or TypeSafe-compatible with https://api.typesafe.ai/v1/systemone. Enter the full POST URL.',
    keywords: 'judge self-hosted local Jev API key provider model probability',
  },
  'default-agent': {
    group: 'sessions',
    title: 'Default agent',
    description: 'Agent used for new conversations unless you choose another agent in the composer. Defaults to build. Choose from the known agents on this machine.',
    example: 'Set plan to start new conversations in planning mode, or build to start implementing immediately.',
    keywords: 'new session option alt t role',
  },
  'system-notifications': {
    group: 'notifications',
    title: 'System notifications',
    description: 'Show a desktop notification when a session finishes or needs your input. Works best after installing ocman as an app.',
    example: 'A long refactor finishes while you are in another app: a "Session done" notification appears, and clicking it opens the session.',
    keywords: 'desktop alert push',
    requires: 'Shown only in browsers that support notifications.',
  },
  'bell-sound': {
    group: 'notifications',
    title: 'Bell sound',
    description: 'Play a bell sound when the app is not in focus and a session finishes or asks a question.',
    example: 'You are reading docs in another tab when the agent asks which branch to use: the bell rings so you know to switch back.',
    keywords: 'audio chime alert',
  },
  'start-screen-time-range': {
    group: 'sessions',
    title: 'Start screen time range',
    description: 'Default lookback window for the Sessions list on the start screen. The time-range buttons still override it for the current view.',
    example: 'Set to 7 days to list only sessions active in the past week when you open ocman.',
    keywords: 'dashboard lookback days history',
  },
  'recent-sessions-window': {
    group: 'sessions',
    title: 'Recent sessions window',
    description: 'How far back the "Recent sessions" sidebar looks while you are inside a session.',
    example: 'Set to 2 days to keep the sidebar short and show only what you touched yesterday and today.',
    keywords: 'sidebar days',
  },
  'message-metadata': {
    group: 'sessions',
    title: 'Message section metadata',
    description: 'Show timestamp, duration, speed, and model after each assistant message section. The summary between turns stays visible.',
    example: 'Turn it on to see "14:02 · 38s · 52 tok/s · claude-sonnet" under each answer section when comparing models.',
    keywords: 'timestamp duration tokens model speed',
  },
  'read-aloud': {
    group: 'sessions',
    title: 'Read answers aloud',
    description: 'Automatically read new final answers in the focused session tab. Off by default. Code blocks, reasoning, and tool output are skipped. Saved for this browser.',
    example: 'Turn it on while cooking: when the agent finishes, its summary is spoken, without the code blocks.',
    keywords: 'speech tts voice audio',
  },
  'reading-voice': {
    group: 'sessions',
    title: 'Reading voice',
    description: 'The voice used to read answers aloud. Local voices stay on your device. Online voices may send answer text to the voice service.',
    example: 'Pick "Samantha (en-US, local)" to keep answer text on this machine.',
    keywords: 'speech tts',
    requires: 'Shown only in browsers that support speech playback.',
  },
  'reading-speed': {
    group: 'sessions',
    title: 'Reading speed',
    description: 'Playback rate for answers read aloud, from 0.5× to 2×. Use Preview voice to hear it.',
    example: '1.3× reads a long summary noticeably faster while staying easy to follow.',
    keywords: 'speech tts rate',
    requires: 'Shown only in browsers that support speech playback.',
  },
  'worktree-inherit-permissions': {
    group: 'sessions',
    title: 'Worktree sessions inherit parent permissions',
    description: 'When you split a session into a worktree, seed the new session with the permissions you already approved with "Allow always" in the parent, so it does not re-prompt for them.',
    example: 'You allowed "go test ./..." always in the parent; the /wt child runs it without asking again.',
    keywords: 'wt split allow always yolo',
  },
  'auto-archive': {
    group: 'sessions',
    title: 'Automatically archive inactive sessions and projects',
    description: 'Hide inactive sessions and projects after the configured number of days. Archived items remain available and can be restored.',
    example: 'A session you last touched two weeks ago drops out of the list; find it again under Archived.',
    keywords: 'cleanup hide ttl',
  },
  'auto-archive-after': {
    group: 'sessions',
    title: 'Archive after',
    description: 'Number of days without activity before a session or project is archived.',
    example: '30 days keeps a month of work visible before it is archived.',
    keywords: 'ttl days inactive',
    requires: 'Shown only when automatic archiving is turned on.',
  },
  'archive-resurface': {
    group: 'sessions',
    title: 'Show archived sessions again',
    description: 'Choose when new activity brings an archived session back to the sidebar. Session halts waits until it is done, waiting, errored, or interrupted.',
    example: 'Archive a running session to hide it until its turn stops.',
    keywords: 'unarchive resurface halt activity done error',
  },
  'model-fallthrough-patience': {
    group: 'sessions',
    title: 'Model fallthrough patience',
    description: 'Minimum time a provider that ran out of quota is skipped before the project model list tries it again.',
    example: 'At 15 min, a provider that hit its rate limit is left alone for at least 15 minutes, even if it reports an earlier reset.',
    keywords: 'quota rate limit provider fallback',
  },
  'model-fallthrough-cooldown': {
    group: 'sessions',
    title: 'Model fallthrough cooldown',
    description: 'How long a provider is skipped when it reports no reset time.',
    example: 'At 60 min, a provider that returns "quota exceeded" without a reset time is skipped for an hour.',
    keywords: 'quota rate limit provider fallback',
  },
  'remotes': {
    group: 'remotes',
    title: 'Attached remotes',
    description: 'Attach other ocman instances to manage their sessions from here. Copy a remote\'s access token from its own Settings page (run it with -remote-listen) and paste it below.',
    example: 'On the build box run "ocman -remote-listen :8230", copy its token, then add "buildbox.lan:8230" here.',
    keywords: 'machine host grpc token multi-remote',
  },
  'plugin-owner': {
    group: 'plugins',
    title: 'Plugin owner',
    description: 'Plugins and their data belong to the selected machine.',
    example: 'Select "buildbox" to manage the plugins installed on that remote rather than on this machine.',
    keywords: 'machine remote',
  },
  'plugin-discovery': {
    group: 'plugins',
    title: 'Discovery',
    description: 'Rescan the selected owner\'s plugin directory for new or changed executables.',
    example: 'After "make install-plugin PLUGIN=slack", click Rescan plugins to list the Slack plugin.',
    keywords: 'rescan refresh health install',
  },
  'plugin-undelivered-replies': {
    group: 'plugins',
    title: 'Undelivered replies',
    description: 'Completed replies are stored before they are sent, so a disconnect or a restart retries them instead of losing them.',
    example: 'Slack was unreachable for a minute: "2 waiting · 1 retrying" until the replies are posted.',
    keywords: 'conversation outbox slack delivery',
    requires: 'Shown only for an installed conversation plugin, such as Slack.',
  },
  'plugin-backlog-limits': {
    group: 'plugins',
    title: 'Backlog limits',
    description: 'At either limit, new conversation work pauses instead of replies being dropped.',
    example: '"40 of 500 replies · 12 of 1024 KiB" means there is plenty of room left.',
    keywords: 'conversation outbox pause',
    requires: 'Shown only for an installed conversation plugin, such as Slack.',
  },
  'plugin-dead-letters': {
    group: 'plugins',
    title: 'Replies awaiting a decision',
    description: 'These exhausted their retries. Later replies in the same conversation wait until each one is retried or discarded.',
    example: 'A reply to a deleted Slack thread failed 6 times; discard it so the conversation continues.',
    keywords: 'dead letter retry discard',
    requires: 'Shown only when a conversation plugin has replies that exhausted their retries.',
  },
  'auto-approve-default': {
    group: 'auto-approve',
    title: 'Enable by default',
    description: 'Automatically start the AI permission reviewer for every new session. You can also enable or disable it per session from the permission prompt.',
    example: 'Turn it on so a new session\'s "npm test" prompt is judged and approved without you clicking Allow.',
    keywords: 'judge permission reviewer',
  },
  'human-review-window': {
    group: 'auto-approve',
    title: 'Human review window',
    description: 'How long to wait after a permission prompt appears before the AI reviewer starts. Gives you time to approve or reject manually.',
    example: 'At 10 s you have ten seconds to answer a prompt yourself before the reviewer decides.',
    keywords: 'delay judge seconds',
  },
  'reviewer-model': {
    group: 'auto-approve',
    title: 'Reviewer model',
    description: 'The model that judges permission prompts. A fast, cheap model is usually the right pick.',
    example: 'Pick "anthropic/claude-haiku" for quick, low-cost decisions.',
    keywords: 'judge llm',
  },
  'reviewer-prompt-sections': {
    group: 'auto-approve',
    title: 'Reviewer prompt sections',
    description: 'Extra rules appended to the AI reviewer\'s prompt. Each section appears as a named block the model reads before deciding. Use this to allow or deny specific patterns your team knows are safe.',
    example: 'Title "Terraform", content "terraform plan is safe; never approve terraform apply or destroy."',
    keywords: 'judge rules policy allow deny',
  },
  'public-sharing': {
    group: 'sharing',
    title: 'Allow public sharing',
    description: 'Let sessions be shared via public, read-only links. When off, no new share links can be created; existing links keep working until revoked below.',
    example: 'Turn it on, then use Share on a session to send a read-only link to a colleague.',
    keywords: 'share link public',
  },
  'share-relay': {
    group: 'sharing',
    title: 'Share relay',
    description: 'The relay that stores encrypted shared conversations so links open from another machine. Set with -relay-url or OCMAN_RELAY_URL; restart ocman to change it.',
    example: 'Start ocman with "-relay-url https://relay.example.com" to share across machines.',
    keywords: 'relay url OCMAN_RELAY_URL',
  },
  'shared-sessions': {
    group: 'sharing',
    title: 'Shared sessions',
    description: 'Every active public share link. Open the session to inspect it, or revoke a link to make it stop working immediately.',
    example: 'Revoke the link you posted in a public channel by mistake; it stops working at once.',
    keywords: 'revoke links',
  },
  'webhook-relay': {
    group: 'webhooks',
    title: 'Webhook relay',
    description: 'Relay that receives provider webhooks for new inboxes. Leave empty to use the share relay. Existing inboxes keep their relay.',
    example: 'Set https://relay.example.com so GitHub can deliver to an inbox while ocman runs behind a firewall.',
    keywords: 'webhook inbox relay url',
  },
  'webhook-enrollment-token': {
    group: 'webhooks',
    title: 'Enrollment token',
    description: 'Authorizes inbox registration on the relay. Stored on this machine and never shown again.',
    example: 'Paste the token your relay operator gave you, then create a webhook inbox.',
    keywords: 'webhook relay token secret',
  },
  'launch-prompt-templates': {
    group: 'templates',
    title: 'Launch prompt templates',
    description: 'The prompt sent to a new agent session when you click "Handle this PR/Issue" in the sidebar. Edit the templates below; placeholders are substituted at launch time.',
    example: '"Review PR #{{number}}: {{title}}. Read the diff and leave comments." becomes a concrete prompt for the PR you picked.',
    keywords: 'pull request issue forge github forgejo placeholder',
  },
  'custom-link-rules': {
    group: 'link-previews',
    tab: 'rules',
    title: 'Custom link rules',
    description: 'Create link cards for ticket IDs and other text patterns. Use $& for the whole match or $1 for the first capture group.',
    example: 'Pattern "ABC-\\d+" with URL "https://tracker.example.com/issues/$&" turns ABC-123 into a link card.',
    keywords: 'regex pattern ticket jira',
  },
  'preview-redirect-uri': {
    group: 'link-previews',
    tab: 'apps',
    title: 'Redirect URI',
    description: 'Register this exact URL with every provider sign-in app.',
    example: 'Paste it as the OAuth redirect URL when creating the Slack app.',
    keywords: 'oauth callback',
  },
  'preview-sign-in-app': {
    group: 'link-previews',
    tab: 'apps',
    title: 'Add a sign-in app',
    description: 'Register an OAuth app for providers without personal tokens, such as Slack and Jira. Saved apps override the environment.',
    example: 'Add the Slack client ID and secret so Slack message links show previews.',
    keywords: 'oauth client slack jira',
  },
  'preview-add-host': {
    group: 'link-previews',
    tab: 'accounts',
    title: 'Add a host',
    description: 'Preview a self-hosted Forgejo or GitLab with a personal token.',
    example: 'Type Forgejo, host "git.example.com", and a token to preview its PR links.',
    keywords: 'forgejo gitlab token provider',
    requires: 'Shown only when a provider supports self-hosted instances.',
  },
  'opencode-database': {
    group: 'maintenance',
    title: 'OpenCode database',
    description: 'Where OpenCode\'s database lives and how large it is.',
    example: '~/.local/share/opencode/opencode.db · 12.4 GB',
    keywords: 'sqlite size disk',
  },
  'remove-old-diffs': {
    group: 'maintenance',
    title: 'Remove old diffs',
    description: 'Remove the per-message file patches of old sessions and compact the database. ocman stops its managed opencode instances while it runs, and keeps the removed patches in a dump so they can be restored.',
    example: 'A 12 GB database shrinks to 3 GB after removing diffs from sessions older than 30 days.',
    keywords: 'cleanup vacuum disk space compact',
  },
  'removed-diffs': {
    group: 'maintenance',
    title: 'Removed diffs',
    description: 'The dump of patches removed by the cleanup. Restore puts them back; deleting the dump makes the cleanup permanent.',
    example: 'Restore if OpenCode\'s per-turn changes view is missing a diff you need.',
    keywords: 'dump restore',
  },
  'open-links-in-chrome': {
    group: 'behaviour',
    title: 'Open external links in Chrome',
    description: 'Requires Google Chrome to be installed. Links will fail to open if it isn\'t.',
    example: 'Installed on an iPad Home Screen, tapping a GitHub link opens it in Chrome instead of Safari.',
    keywords: 'browser safari ios ipad iphone',
    requires: 'Shown only on iPhone and iPad.',
  },
  'install-app': {
    group: 'app',
    title: 'Install ocman',
    description: 'Install ocman as a standalone app with its own window and dock icon. The web version keeps working in any browser tab.',
    example: 'After installing, launch ocman from the dock and it opens in its own window.',
    keywords: 'pwa desktop dock',
  },
  'sign-out': {
    group: 'account',
    title: 'Sign out',
    description: 'Sign out of the current session.',
    example: 'Sign out on a shared computer before you leave.',
    keywords: 'logout sign out password',
  },
  'project-add-model': {
    group: 'project',
    title: 'Add model',
    description: 'Available or previously used models on the selected machine.',
    example: 'Add "openai/gpt-5" as a fallback below the default.',
  },
} satisfies Record<string, SettingEntry>;

export type SettingId = keyof typeof SETTINGS;

export function settingEntry(id: SettingId): SettingEntry {
  return SETTINGS[id];
}

/** DOM id of a catalogued setting's row, used to jump to a search result. */
export function settingAnchor(id: SettingId) {
  return `setting-${id}`;
}

/**
 * searchSettings returns the entries matching every word of `query` in
 * their title, description, example, keywords or group label. Title
 * matches sort first.
 */
export function searchSettings(
  query: string,
  groupLabels: Partial<Record<SettingsGroupId, string>>,
): Array<{ id: SettingId } & SettingEntry> {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return [];
  const hits: Array<{ hit: { id: SettingId } & SettingEntry; rank: number }> = [];
  for (const [id, entry] of Object.entries(SETTINGS) as Array<[SettingId, SettingEntry]>) {
    const group = groupLabels[entry.group];
    if (group === undefined) continue; // group not shown here
    const title = entry.title.toLowerCase();
    const text = [title, entry.description, entry.example, entry.keywords ?? '', group].join(' ').toLowerCase();
    if (!words.every((w) => text.includes(w))) continue;
    hits.push({ hit: { id, ...entry }, rank: words.every((w) => title.includes(w)) ? 0 : 1 });
  }
  return hits.sort((a, b) => a.rank - b.rank).map((h) => h.hit);
}
