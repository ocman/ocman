import { describe, expect, it } from 'vitest';
import goDtos from './go-dtos.json';
import type {
  HostCapabilities, Message, Part, PartData, PlatformCapabilities, QueuedMessage, Session, SessionNotice, SessionStatus,
} from '../api.types';
import type { QuestionItem } from '../../components/session/QuestionPrompt';
import type { BackgroundTask } from '../sessionBackgroundTasks';
import type * as Oce from '../conversation/events';

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
    lastMessageAt: true, branch: true, origin: true, originId: true,
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
    composer: false, respondPermission: false, respondQuestion: false, abort: false, backgroundTaskStop: true, compact: false, fork: false,
    forkAtMessage: true, move: false, events: false, agentCatalog: false, modelCatalog: false, slashCommands: false,
    shellExec: false, fileChanges: false, sessionInfo: false, liveConnectionHint: true, autoApprove: false,
    permissionRules: false, nativeAutoPermission: true, exactPatterns: true, revert: true, runtimeRestart: true, pushPrompts: true, autoTitle: true,
  }),
  HostCapabilities: flags<HostCapabilities>()({
    gitDiff: false, worktrees: false, tmux: false, projects: false, whisper: false, opencodeLaunch: false, opencode: true,
  }),
  QueuedMessage: flags<QueuedMessage>()({
    id: false, text: false, hasImages: false, model: true, agent: true, reasoning: true, createdAt: false,
  }),

  // Conversation events (lib/conversation/events.ts).
  OceEnvelope: flags<Oce.OceEnvelope>()({
    v: false, epoch: false, seq: false, sessionId: false, platform: false, at: false, durability: false, replayed: true, event: false,
  }),
  OcePart: flags<Oce.OcePart>()({ id: false, messageId: false, sessionId: false, timeCreated: true, position: true, data: false }),
  PartData: flags<PartData>()({
    type: false, synthetic: true, ignored: true, callID: true, text: true, tool: true, time: true, permission: true,
    patterns: true, reasoning: true, approvedBy: true, reply: true, metadata: true, askedAt: true, approvedAt: true,
    mime: true, url: true, filename: true, state: true, file: true, path: true, content: true, diff: true,
  }),
  PartState: flags<NonNullable<PartData['state']>>()({
    status: true, time: true, input: true, output: true, error: true, title: true, metadata: true, attachments: true, raw: true,
  }),
  PartTime: flags<NonNullable<PartData['time']>>()({ start: true, end: true }),
  OcePrompt: flags<Oce.OcePrompt>()({
    id: false, kind: false, sessionId: false, askedAt: false, permission: true, question: true,
  }),
  OcePermissionRequest: flags<Oce.OcePermissionRequest>()({ permission: false, patterns: false, always: true, metadata: true }),
  OceQuestionRequest: flags<Oce.OceQuestionRequest>()({ questions: false }),
  QuestionItem: flags<QuestionItem>()({ question: false, header: false, options: false, multiple: true, custom: true }),
  QuestionOption: flags<QuestionItem['options'][number]>()({ label: false, description: false }),
  OceChild: flags<Oce.OceChild>()({
    id: false, parentId: true, title: false, timeCreated: false, timeUpdated: false, agent: true, deleted: true,
  }),
  OceSessionPatch: flags<Oce.OceSessionPatch>()({
    title: true, summaryAdditions: true, summaryDeletions: true, summaryFiles: true, shareUrl: true, archived: true,
    defaultAgent: true, defaultModel: true, revert: true,
  }),
  OceRevertMarker: flags<Oce.OceRevertMarker>()({ messageId: false, partId: true }),
  OceErrorInfo: flags<Oce.OceErrorInfo>()({ name: true, message: false, statusCode: true }),
  SessionBackgroundTask: flags<BackgroundTask>()({ id: false, description: false, kind: true }),
  OceInflight: flags<Oce.OceInflight>()({ messages: false, parts: false }),
  LiveOverlay: flags<Oce.LiveOverlay>()({
    status: false, prompts: false, judge: false, background: false, queue: true, inflight: false, liveConnection: false,
  }),
  OceStreamCapabilities: flags<Oce.OceStreamCapabilities>()({ streamResume: false, deltaFields: false }),
  StreamHello: flags<Oce.StreamHello>()({ type: false, protocol: false, resumed: false, overlay: true, revision: false }),
  StreamReset: flags<Oce.StreamReset>()({ type: false, reason: false }),
  StreamError: flags<Oce.StreamError>()({ type: false, code: false, message: false, retryable: false }),
  MessageUpserted: flags<Oce.MessageUpserted>()({ type: false, message: false, position: true }),
  MessageRemoved: flags<Oce.MessageRemoved>()({ type: false, messageId: false, reason: false }),
  PartUpserted: flags<Oce.PartUpserted>()({ type: false, part: false, final: false }),
  PartRemoved: flags<Oce.PartRemoved>()({ type: false, messageId: false, partId: false }),
  PartDelta: flags<Oce.PartDelta>()({
    type: false, messageId: false, partId: false, field: false, offset: false, text: false, partType: true,
  }),
  SessionStatusEvent: flags<Oce.SessionStatusEvent>()({ type: false, status: false, provisional: true, notice: true, resetsAt: true }),
  TurnStarted: flags<Oce.TurnStarted>()({ type: false, startedAt: false, messageId: true }),
  TurnEnded: flags<Oce.TurnEnded>()({ type: false, outcome: false, endedAt: false, latestMessageId: true }),
  SessionUpdated: flags<Oce.SessionUpdated>()({ type: false, patch: false }),
  HistoryRewritten: flags<Oce.HistoryRewritten>()({ type: false, reason: false, fromMessageId: true }),
  HistoryCommitted: flags<Oce.HistoryCommitted>()({ type: false, revision: false }),
  PromptAsked: flags<Oce.PromptAsked>()({ type: false, prompt: false }),
  PromptJudge: flags<Oce.PromptJudge>()({ type: false, promptId: false, phase: false, startsAt: true, reasoning: true }),
  PromptResolved: flags<Oce.PromptResolved>()({ type: false, promptId: false, kind: false, outcome: false, by: false }),
  QueueChanged: flags<Oce.QueueChanged>()({ type: false, messages: false }),
  BackgroundChanged: flags<Oce.BackgroundChanged>()({ type: false, count: false, tasks: false }),
  ChildUpserted: flags<Oce.ChildUpserted>()({ type: false, child: false }),
  ChildStatus: flags<Oce.ChildStatus>()({ type: false, childId: false, status: false, notice: true }),
  ChangesDirty: flags<Oce.ChangesDirty>()({ type: false, reason: false }),
  SessionError: flags<Oce.SessionError>()({ type: false, messageId: true, error: false }),
};

// Every OCE event interface must be in the drift check above.
const oceEventTypes = keysOf<Record<Oce.OceEventType, true>>()({
  'stream.hello': true, 'stream.reset': true, 'stream.error': true, 'message.upserted': true, 'message.removed': true,
  'part.upserted': true, 'part.removed': true, 'part.delta': true, 'session.status': true, 'turn.started': true,
  'turn.ended': true, 'session.updated': true, 'history.rewritten': true, 'history.committed': true,
  'prompt.asked': true, 'prompt.judge': true, 'prompt.resolved': true, 'queue.changed': true,
  'background.changed': true, 'child.upserted': true, 'child.status': true, 'changes.dirty': true, 'session.error': true,
});

const statuses = keysOf<Record<SessionStatus, true>>()({
  busy: true, limited: true, waiting: true, done: true, error: true, interrupted: true,
});

describe('frontend API types match the Go DTOs (go-dtos.json)', () => {
  it('checks every OCE event type', () => {
    const checked = Object.keys(goDtos.dtos).filter((name) => (goDtos.dtos[name as keyof typeof goDtos.dtos])
      .some((f) => f.name === 'type') && name !== 'PartData');
    expect(checked.length).toBe(Object.keys(oceEventTypes).length);
  });

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
