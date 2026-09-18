---
title: Read answers aloud
weight: 65
---

Use the speaker icon at the end of a completed assistant turn to read its final
answer aloud. The icon changes to **Stop** during playback. The bookmark icon
beside it bookmarks the turn's last assistant message.

Only the final answer's text is spoken. Reasoning, progress messages, tool calls,
command output, code blocks, images, and compaction summaries are excluded.
Links are read by their labels and inline code is kept, so commands mentioned in
ordinary prose can still be spoken. Failed, interrupted, and tool-only endings
do not have a speaker button.

Under **Settings → Sessions**, enable **Read answers aloud** to automatically
read new answers in the focused session tab. This is off by default and saved
for the current browser. Opening history or reconnecting does not replay old
answers. Playback stops when you send another prompt, leave the session, or
disable automatic reading.

The same settings offer a voice selector, reading speed, and voice preview.
Voices come from your browser or operating system. Automatic selection prefers
a local voice matching the browser language. Voices marked **online** may send
answer text to their speech service. Ocman does not run a TTS server or store
audio files.

Voice availability and automatic playback vary by browser and desktop webview.
If playback is blocked, use the speaker button or preview the voice in Settings.
Browsers without speech synthesis hide the speaker and disable automatic reading.
