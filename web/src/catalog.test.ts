import { describe, expect, it } from 'vitest';
import { detectMediaType, humanBytes } from './catalog';

function file(name: string, type: string) {
  return new File(['x'], name, { type });
}

describe('catalog helpers', () => {
  it('detects media type from MIME or extension', () => {
    expect(detectMediaType(file('clip.mp4', 'video/mp4'))).toBe('video');
    expect(detectMediaType(file('song.flac', ''))).toBe('audio');
    expect(detectMediaType(file('photo.webp', ''))).toBe('image');
  });

  it('formats bytes for display', () => {
    expect(humanBytes(1024)).toBe('1.0 KB');
    expect(humanBytes(10 * 1024 * 1024)).toBe('10 MB');
  });
});
