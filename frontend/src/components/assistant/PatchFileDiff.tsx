// One file of an apply_patch diff. @pierre/diffs is heavy, so renderPatch
// loads this module only when a thread shows a patch.
import { PatchDiff } from '@pierre/diffs/react';
import { DIFF_OPTIONS } from '../diffOptions';

const PATCH_OPTIONS = { ...DIFF_OPTIONS, disableFileHeader: true };

export function PatchFileDiff({ patch }: { patch: string }) {
  return <PatchDiff patch={patch} options={PATCH_OPTIONS} disableWorkerPool />;
}
