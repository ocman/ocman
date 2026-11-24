import type { Part, PartData } from './api';
import { parsePart } from './parsePart';

export function partStartedAt(part: Part): number {
  return part.timeCreated || parsePart(part).time?.start || 0;
}

function containsSteps(command: string, steps: string[]): boolean {
  let offset = 0;
  for (const step of steps) {
    const index = command.indexOf(step, offset);
    if (index < 0) return false;
    offset = index + step.length;
  }
  return true;
}

function activeShellCommand(part: Part, askedAt: number): unknown {
  const { tool, state, time } = parsePart(part);
  if (tool?.toLowerCase().replace(/^mcp_/, '') !== 'bash') return;
  const end = state?.time?.end || time?.end;
  if (end ? end < askedAt : state?.status !== 'running') return;
  return state?.input?.command;
}

// A compound Bash permission can join parsed shell steps differently from the
// transcript (for example, joining a pipeline with &&). Only use those steps
// for display association, and only when they identify one active call.
export function matchCompoundShellApproval(parts: Part[], approval: PartData): Part | undefined {
  const { askedAt, patterns, metadata } = approval;
  if (!askedAt || !patterns || patterns.length < 2 || patterns.some((step) => !step.trim())
    || typeof metadata?.command !== 'string' || !containsSteps(metadata.command, patterns)) return;

  let match: Part | undefined;
  for (const part of parts) {
    if (partStartedAt(part) > askedAt) break;
    const command = activeShellCommand(part, askedAt);
    if (typeof command !== 'string' || !containsSteps(command, patterns)) continue;
    if (match) return;
    match = part;
  }
  return match;
}
