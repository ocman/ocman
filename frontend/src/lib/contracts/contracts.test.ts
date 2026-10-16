import { describe, expect, it } from 'vitest';
import goDtos from './go-dtos.json';
import type { HostCapabilities, Message, Part, PlatformCapabilities, Session, SessionNotice, SessionStatus } from '../api.types';

// Drift check between the hand-written API types and the Go DTOs they mirror.
// go-dtos.json is written by internal/frontendcontract (`make contracts`) and
// that Go test fails when it is stale. Each key list below must name exactly
// the keys of its TypeScript type (the compiler enforces that), and the test
// compares them with the Go JSON fields.

type OptionalKeys<T> = { [K in keyof T]-?: object extends Pick<T, K> ? K : never }[keyof T];

/** Every key of T, checked at compile time, as a runtime list. */
function keysOf<T>() {
  return <R extends Record<keyof T, boolean>>(keys: R & Record<Exclude<keyof R, keyof T>, never>) => keys;
}

// `true` marks a key optional in TypeScript; the compiler checks the flags.
type Flags<T> = { [K in keyof T]-?: K extends OptionalKeys<T> ? true : false };
const flags = <T,>() => <R extends Flags<T>>(r: R & Record<Exclude<keyof R, keyof T>, never>) => keysOf<T>()(r);

const ts: Record<keyof typeof goDtos.dtos, Record<string, boolean>> = {
  Session: flags<Session>()({
    id: false, platform: false, projectId: false, parentId: true, title: false, directory: false,
    timeCreated: false, timeUpdated: false, lastTurnCompletedAt: true, lastUserPromptAt: true, lastHaltAt: true,
    summaryAdditions: false, summaryDeletions: false, summaryFiles: false, shareUrl: false, messageCount: false,
    durationMs: false, activeDurationMs: false, totalInputTokens: false, totalOutputTokens: false, totalCost: false,
    totalEstCost: true, totalEffectiveCost: true, status: false, resetsAt: true, liveConnection: false,
    pendingPermission: false, pendingQuestion: false, archived: false, snoozedUntil: true, seen: false, pinned: false,
    pinnedAt: false, routineId: true, factoryAttemptId: true, seenTimeUpdated: false, unreadCount: false, notice: true,
    projectDefaultModel: true, remoteId: true, remoteName: true, stale: true,
  }),
  SessionNotice: flags<SessionNotice>()({ kind: false, message: false, retryAt: false, attempt: false }),
  Message: flags<Message>()({ id: false, sessionId: false, timeCreated: false, data: false }),
  Part: flags<Part>()({ id: false, messageId: false, sessionId: false, timeCreated: true, data: false }),
  PlatformCapabilities: flags<PlatformCapabilities>()({
    composer: false, respondPermission: false, respondQuestion: false, abort: false, compact: false, fork: false,
    forkAtMessage: true, move: false, events: false, agentCatalog: false, modelCatalog: false, slashCommands: false,
    shellExec: false, fileChanges: false, sessionInfo: false, liveConnectionHint: true, autoApprove: false,
    permissionRules: false, exactPatterns: true, revert: true, runtimeRestart: true, pushPrompts: true, autoTitle: true,
  }),
  HostCapabilities: flags<HostCapabilities>()({
    gitDiff: false, worktrees: false, tmux: false, projects: false, whisper: false, opencodeLaunch: false, opencode: true,
  }),
};

const statuses = keysOf<Record<SessionStatus, true>>()({
  busy: true, limited: true, waiting: true, done: true, error: true, interrupted: true,
});

describe('frontend API types match the Go DTOs (go-dtos.json)', () => {
  it('lists the same session statuses', () => {
    expect(Object.keys(statuses).sort()).toEqual(goDtos.SessionStatus);
  });

  it.each(Object.keys(goDtos.dtos) as (keyof typeof goDtos.dtos)[])('%s has the same fields', (name) => {
    const fields = goDtos.dtos[name];
    expect(Object.keys(ts[name]).sort()).toEqual(fields.map((f) => f.name).sort());
    // A field Go may omit must be optional in TypeScript.
    const omitted = fields.filter((f) => f.omitempty).map((f) => f.name);
    expect(omitted.filter((key) => !ts[name][key])).toEqual([]);
  });
});
