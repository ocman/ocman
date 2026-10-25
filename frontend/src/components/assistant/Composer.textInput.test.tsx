// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useShortcutDispatcher } from '../../lib/shortcutRegistry';
import { Composer } from './Composer';

function WithShortcuts() {
  useShortcutDispatcher();
  return <Composer isRunning={false} onSend={vi.fn()} />;
}

afterEach(() => vi.unstubAllGlobals());

it('leaves Alt+D to external tools without requesting microphone access', () => {
  const recognition = vi.fn();
  const getUserMedia = vi.fn();
  vi.stubGlobal('isSecureContext', true);
  vi.stubGlobal('SpeechRecognition', recognition);
  vi.stubGlobal('webkitSpeechRecognition', recognition);
  vi.stubGlobal('navigator', { platform: navigator.platform, mediaDevices: { getUserMedia } });
  render(<WithShortcuts />);
  const input = screen.getByRole('textbox');
  fireEvent.input(input, { target: { value: 'Text from an external tool' } });
  expect(fireEvent.keyDown(input, { code: 'KeyD', altKey: true })).toBe(true);
  expect(recognition).not.toHaveBeenCalled();
  expect(getUserMedia).not.toHaveBeenCalled();
  expect(input).toHaveValue('Text from an external tool');
  expect(screen.queryByRole('button', { name: /record|dictation/i })).not.toBeInTheDocument();
});
