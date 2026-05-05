import { describe, it, expect } from 'vitest';
import { encodeWav } from './encodeWav';

/**
 * Read a 32-bit unsigned little-endian integer from a Uint8Array. Used
 * to verify the chunk size + sample rate fields embedded in the WAV
 * header without re-parsing through DataView in every test.
 */
function readUint32LE(bytes: Uint8Array, offset: number): number {
  return bytes[offset] | (bytes[offset + 1] << 8) | (bytes[offset + 2] << 16) | (bytes[offset + 3] << 24);
}

function readUint16LE(bytes: Uint8Array, offset: number): number {
  return bytes[offset] | (bytes[offset + 1] << 8);
}

function readInt16LE(bytes: Uint8Array, offset: number): number {
  const v = readUint16LE(bytes, offset);
  return v >= 0x8000 ? v - 0x10000 : v;
}

function blobBytes(blob: Blob): Promise<Uint8Array> {
  return blob.arrayBuffer().then((b) => new Uint8Array(b));
}

describe('encodeWav', () => {
  it('produces a Blob with audio/wav MIME', () => {
    const blob = encodeWav(new Float32Array(0), 16_000);
    expect(blob.type).toBe('audio/wav');
  });

  it('writes the RIFF/WAVE header with the expected fields', async () => {
    const samples = new Float32Array(10);
    const blob = encodeWav(samples, 16_000);
    const bytes = await blobBytes(blob);

    // "RIFF" magic
    expect(String.fromCharCode(...bytes.slice(0, 4))).toBe('RIFF');
    // chunkSize = 36 + numSamples * 2
    expect(readUint32LE(bytes, 4)).toBe(36 + 10 * 2);
    // "WAVE" + "fmt "
    expect(String.fromCharCode(...bytes.slice(8, 12))).toBe('WAVE');
    expect(String.fromCharCode(...bytes.slice(12, 16))).toBe('fmt ');
    // subchunk1Size = 16, audioFormat = 1 (PCM), numChannels = 1
    expect(readUint32LE(bytes, 16)).toBe(16);
    expect(readUint16LE(bytes, 20)).toBe(1);
    expect(readUint16LE(bytes, 22)).toBe(1);
    // sample rate
    expect(readUint32LE(bytes, 24)).toBe(16_000);
    // byte rate = sampleRate * 2
    expect(readUint32LE(bytes, 28)).toBe(32_000);
    // block align = 2, bits per sample = 16
    expect(readUint16LE(bytes, 32)).toBe(2);
    expect(readUint16LE(bytes, 34)).toBe(16);
    // "data" + dataSize
    expect(String.fromCharCode(...bytes.slice(36, 40))).toBe('data');
    expect(readUint32LE(bytes, 40)).toBe(10 * 2);
  });

  it('encodes samples as 16-bit little-endian PCM', async () => {
    const samples = new Float32Array([0, 1, -1, 0.5, -0.5]);
    const blob = encodeWav(samples, 8_000);
    const bytes = await blobBytes(blob);

    expect(readInt16LE(bytes, 44)).toBe(0);          // 0 -> 0
    expect(readInt16LE(bytes, 46)).toBe(0x7FFF);     // +1 -> 32767
    expect(readInt16LE(bytes, 48)).toBe(-0x8000);    // -1 -> -32768
    // setInt16 truncates toward zero, so 0.5 * 32767 = 16383.5 → 16383.
    expect(readInt16LE(bytes, 50)).toBe(Math.trunc(0.5 * 0x7FFF));
    // -0.5 * 32768 = -16384 (already an integer).
    expect(readInt16LE(bytes, 52)).toBe(Math.trunc(-0.5 * 0x8000));
  });

  it('clips samples outside the [-1, 1] range', async () => {
    const samples = new Float32Array([2, -2]);
    const blob = encodeWav(samples, 8_000);
    const bytes = await blobBytes(blob);

    // 2 -> clipped to 1 -> 0x7FFF
    expect(readInt16LE(bytes, 44)).toBe(0x7FFF);
    // -2 -> clipped to -1 -> -0x8000
    expect(readInt16LE(bytes, 46)).toBe(-0x8000);
  });

  it('produces the right buffer size for any sample count', async () => {
    for (const n of [0, 1, 100, 1_024]) {
      const blob = encodeWav(new Float32Array(n), 16_000);
      const bytes = await blobBytes(blob);
      expect(bytes.length).toBe(44 + n * 2);
    }
  });
});
