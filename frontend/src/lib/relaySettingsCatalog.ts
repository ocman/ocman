import type { SettingEntry } from './settingsCatalog';

export const RELAY_SETTINGS = {
  'usage-collection': {
    group: 'privacy', title: 'Send usage reports',
    description: 'Enabled by default. After foreground use, send a daily report containing a persistent random installation ID, UTC activity date and ocman release version to the configured relay. Failed delivery may retry. This counts installations, not people. No prompts, code, paths, repository names, account identifiers or session IDs are sent.',
    example: 'Turn this off to stop usage reports for this instance and clear pending reports. Sharing and webhooks keep working. Previously received records are not deleted.',
    keywords: 'privacy telemetry analytics metrics active users opt out opt-out collection version tracking pseudonymous',
  },
  'public-sharing': {
    group: 'relay', title: 'Allow public sharing',
    description: 'Let sessions be shared via public, read-only links. When off, no new share links can be created; existing links keep working until revoked below.',
    example: 'Turn it on, then use Share on a session to send a read-only link to a colleague.',
    keywords: 'share link public',
  },
  'share-relay': {
    group: 'relay', title: 'Share relay',
    description: 'The relay that stores encrypted shared conversations so links open from another machine. Set with -relay-url or OCMAN_RELAY_URL; restart ocman to change it. Usage reports use this relay too, independently of the sharing toggle.',
    example: 'Start ocman with "-relay-url https://relay.example.com" to share across machines.',
    keywords: 'relay url OCMAN_RELAY_URL',
  },
  'shared-sessions': {
    group: 'relay', title: 'Shared sessions',
    description: 'Every active public share link. Open the session to inspect it, or revoke a link to make it stop working immediately.',
    example: 'Revoke the link you posted in a public channel by mistake; it stops working at once.',
    keywords: 'revoke links',
  },
  'webhook-relay': {
    group: 'relay', title: 'Webhook relay',
    description: 'Relay that receives provider webhooks for new inboxes. Leave empty to use the share relay. Existing inboxes keep their relay.',
    example: 'Set https://relay.example.com so GitHub can deliver to an inbox while ocman runs behind a firewall.',
    keywords: 'webhook inbox relay url',
  },
  'webhook-enrollment-token': {
    group: 'relay', title: 'Relay connection',
    description: 'Sign in through the relay to authorize webhook inbox registration. The enrollment token stays on this machine. Self-hosted relays can also use manual enrollment.',
    example: 'Connect the relay, choose GitHub or Google on its sign-in page, then return here to create an inbox.',
    keywords: 'webhook relay token secret sso login google github',
  },
} satisfies Record<string, SettingEntry>;
