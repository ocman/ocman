import { useState } from 'react';
import type { AttachedImage } from '../../components/assistant/useComposerAttachments';
import { ImageDisplay } from '../../components/AssistantThread';
import { TimelineMarker } from '../../components/assistant/TimelineMarker';
import { LaunchSetup, type StartSteps } from './LaunchSetup';
export type { StartSteps } from './LaunchSetup';

export function StartProgress({ prompt, images, steps, platform, timestamp, complete }: {
  prompt: string; images?: AttachedImage[]; steps: StartSteps; platform?: string; timestamp?: number; complete?: boolean;
}) {
  const [startedAt] = useState(Date.now);
  return <>
    <TimelineMarker timestamp={timestamp ?? startedAt} testId="launch-timestamp" />
    <div className="oc-msg oc-msg-user" data-testid="pending-prompt">
      <div className="oc-msg-body oc-md">
        <span style={{ whiteSpace: 'pre-wrap' }}>{prompt}</span>
        {images?.map((image, index) => <ImageDisplay key={index} image={image.url} />)}
      </div>
    </div>
    <LaunchSetup steps={steps} platform={platform} complete={complete} />
  </>;
}
