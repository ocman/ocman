import { describe, expect, it } from 'vitest';
import { numberKeyIndex, type PromptKeyEvent } from './promptKeyboard';

const key = (code: string, key: string) => ({ code, key }) as PromptKeyEvent;

describe('numberKeyIndex', () => {
  it.each([
    ['Digit1', '1', 0],
    ['Numpad3', '3', 2],
    ['', '2', 1],
    ['Digit4', '$', 3],
    ['Digit9', '9', -1],
    ['KeyA', 'a', -1],
    ['', '0', -1],
  ])('maps %s/%s to %i of four options', (code, value, index) => {
    expect(numberKeyIndex(key(code, value), 4)).toBe(index);
  });
});
