import { describe, expect, it } from 'vitest';
import {
  audioCodecSupportsBitrate,
  detectMediaType,
  formatById,
  humanBytes,
  presetEffectFor,
  presetOptionLabel,
  presetSummaryRows,
  resetInvalidCodecOptions,
  type Catalog
} from './catalog';

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

  it('resets codec choices when the target container changes', () => {
    const catalog = codecCatalog();
    const options: Record<string, string | number | boolean> = { videoCodec: 'h265', audioCodec: 'aac', audioBitrate: 128 };
    resetInvalidCodecOptions(options, formatById(catalog, 'webm'));
    expect(options.videoCodec).toBe('');
    expect(options.audioCodec).toBe('');
    expect(options.audioBitrate).toBe(128);
  });

  it('clears bitrate and codec options for GIF targets', () => {
    const catalog = codecCatalog();
    const options: Record<string, string | number | boolean> = { videoCodec: 'h264', audioCodec: 'aac', videoBitrate: 2500, audioBitrate: 128 };
    resetInvalidCodecOptions(options, formatById(catalog, 'gif'));
    expect(options.videoCodec).toBe('');
    expect(options.audioCodec).toBe('');
    expect(options.videoBitrate).toBeUndefined();
    expect(options.audioBitrate).toBeUndefined();
  });

  it('detects audio codec bitrate support', () => {
    const catalog = codecCatalog();
    expect(audioCodecSupportsBitrate(formatById(catalog, 'mkv'), 'flac')).toBe(false);
    expect(audioCodecSupportsBitrate(formatById(catalog, 'mkv'), 'opus')).toBe(true);
    expect(audioCodecSupportsBitrate(formatById(catalog, 'mp4'), '')).toBe(true);
  });

  it('keeps AV1 as a single user-facing option', () => {
    const catalog = codecCatalog();
    const mp4 = formatById(catalog, 'mp4');
    const av1Options = mp4?.videoCodecs?.filter((codec) => codec.id === 'av1') || [];
    expect(av1Options).toHaveLength(1);
    expect(av1Options[0].label).toBe('AV1');
    expect(av1Options[0].encoder).toBe('libsvtav1');
  });

  it('selects GIF preset effects for GIF output', () => {
    const catalog = codecCatalog();
    const effect = presetEffectFor(catalog, 'balanced', 'video', 'gif');
    expect(effect?.values.maxWidth).toBe(480);
    expect(effect?.values.framerate).toBe(15);
  });

  it('selects video preset effects for video containers', () => {
    const catalog = codecCatalog();
    const effect = presetEffectFor(catalog, 'balanced', 'video', 'mp4');
    expect(effect?.values.maxHeight).toBe(720);
  });

  it('summarizes default and overridden video preset values', () => {
    const catalog = codecCatalog();
    const format = formatById(catalog, 'mp4');
    const effect = presetEffectFor(catalog, 'balanced', 'video', 'mp4');
    const defaults = presetSummaryRows('video', {}, format, effect);
    expect(defaults).toContainEqual({ label: 'Max height', value: '720p' });
    expect(defaults).toContainEqual({ label: 'Video codec', value: 'Auto (H.264)' });
    expect(defaults.some((row) => row.value.includes('libx264') || row.value.includes('libsvtav1'))).toBe(false);

    const overridden = presetSummaryRows('video', { maxHeight: 1080, videoCodec: 'av1' }, format, effect);
    expect(overridden).toContainEqual({ label: 'Max height', value: 'Overridden to 1080p' });
    expect(overridden).toContainEqual({ label: 'Video codec', value: 'AV1' });
  });

  it('summarizes GIF width, FPS, and loop state', () => {
    const catalog = codecCatalog();
    const format = formatById(catalog, 'gif');
    const effect = presetEffectFor(catalog, 'balanced', 'video', 'gif');
    const rows = presetSummaryRows('video', { loop: false }, format, effect);
    expect(rows).toContainEqual({ label: 'Width', value: '480px' });
    expect(rows).toContainEqual({ label: 'FPS', value: '15 FPS' });
    expect(rows).toContainEqual({ label: 'Loop', value: 'Off' });
  });

  it('summarizes audio presets as no audio changes', () => {
    const catalog = codecCatalog();
    const format = formatById(catalog, 'mp3');
    const effect = presetEffectFor(catalog, 'balanced', 'audio', 'mp3');
    const rows = presetSummaryRows('audio', {}, format, effect);
    expect(rows).toContainEqual({ label: 'Preset effect', value: 'No audio changes' });
    expect(rows).toContainEqual({ label: 'Audio bitrate', value: 'Auto unless changed' });
  });

  it('uses target output kind for video inputs converted to audio', () => {
    const catalog = codecCatalog();
    const format = formatById(catalog, 'mp3');
    const effect = presetEffectFor(catalog, 'balanced', 'video', 'mp3');
    const rows = presetSummaryRows('video', {}, format, effect);
    expect(effect?.summary).toBe('Does not change audio settings by itself.');
    expect(rows).toContainEqual({ label: 'Preset effect', value: 'No audio changes' });
    expect(rows.some((row) => row.label === 'Max height')).toBe(false);
    expect(presetOptionLabel(catalog, 'balanced', 'video', 'mp3')).toBe('Balanced - no preset audio changes');
  });

  it('uses target output kind for video inputs converted to images', () => {
    const catalog = codecCatalog();
    const format = formatById(catalog, 'jpg');
    const effect = presetEffectFor(catalog, 'high', 'video', 'jpg');
    const rows = presetSummaryRows('video', {}, format, effect);
    expect(effect?.values.quality).toBe(95);
    expect(rows).toContainEqual({ label: 'Quality', value: '95%' });
    expect(rows.some((row) => row.label === 'Max height')).toBe(false);
    expect(presetOptionLabel(catalog, 'high', 'video', 'jpg')).toBe('High - 95% quality');
  });

  it('clears stale options that do not apply to the selected target kind', () => {
    const catalog = codecCatalog();
    const audioOptions: Record<string, string | number | boolean> = {
      maxHeight: 720,
      videoBitrate: 2500,
      videoCodec: 'h264',
      audioCodec: 'aac',
      audioBitrate: 128
    };
    resetInvalidCodecOptions(audioOptions, formatById(catalog, 'mp3'));
    expect(audioOptions.maxHeight).toBeUndefined();
    expect(audioOptions.videoBitrate).toBeUndefined();
    expect(audioOptions.videoCodec).toBe('');
    expect(audioOptions.audioCodec).toBe('');
    expect(audioOptions.audioBitrate).toBe(128);

    const imageOptions: Record<string, string | number | boolean> = { maxHeight: 720, quality: 86, maxWidth: 1280, audioBitrate: 128 };
    resetInvalidCodecOptions(imageOptions, formatById(catalog, 'jpg'));
    expect(imageOptions.maxHeight).toBeUndefined();
    expect(imageOptions.audioBitrate).toBeUndefined();
    expect(imageOptions.quality).toBe(86);
    expect(imageOptions.maxWidth).toBe(1280);
  });

  it('makes preset differences visible in option labels', () => {
    const catalog = codecCatalog();
    expect(presetOptionLabel(catalog, 'small', 'video', 'mp4')).toBe('Small - max 480p');
    expect(presetOptionLabel(catalog, 'balanced', 'video', 'gif')).toBe('Balanced - 480px / 15 FPS');
    expect(presetOptionLabel(catalog, 'high', 'image', 'jpg')).toBe('High - 95% quality');
    expect(presetOptionLabel(catalog, 'balanced', 'audio', 'mp3')).toBe('Balanced - no preset audio changes');
  });

  it('falls back to built-in preset metadata if the backend is old', () => {
    const catalog = { formats: codecCatalog().formats, presets: ['small', 'balanced', 'high'] };
    expect(presetOptionLabel(catalog, 'high', 'video', 'mp4')).toBe('High - max 1080p');
    const effect = presetEffectFor(catalog, 'small', 'video', 'mp4');
    expect(effect?.values.maxHeight).toBe(480);
  });
});

function codecCatalog(): Catalog {
  return {
    presets: ['balanced'],
    presetDetails: [
      {
        id: 'balanced',
        label: 'Balanced',
        summary: 'Good default quality with moderate output size.',
        effects: {
          video: {
            summary: 'Limits video height to 720p unless overridden.',
            details: [{ label: 'Max height', value: '720p' }],
            values: { maxHeight: 720 }
          },
          gif: {
            summary: 'Creates a 480px wide GIF at 15 FPS.',
            details: [{ label: 'Width', value: '480px' }],
            values: { maxWidth: 480, framerate: 15, loop: true }
          },
          image: {
            summary: 'Uses 86% image quality unless overridden.',
            details: [{ label: 'Quality', value: '86%' }],
            values: { quality: 86 }
          },
          audio: {
            summary: 'Does not change audio settings by itself.',
            details: [{ label: 'Preset effect', value: 'No audio changes' }],
            values: {}
          }
        }
      }
    ],
    formats: [
      {
        id: 'mp4',
        label: 'MP4',
        mediaType: 'video',
        inputs: ['video'],
        videoCodecs: [
          { id: 'h264', label: 'H.264', encoder: 'libx264', available: true, recommended: true },
          { id: 'h265', label: 'H.265 / HEVC', encoder: 'libx265', available: true },
          { id: 'av1', label: 'AV1', encoder: 'libsvtav1', available: true }
        ],
        audioCodecs: [{ id: 'aac', label: 'AAC', encoder: 'aac', available: true, recommended: true, supportsBitrate: true }]
      },
      {
        id: 'webm',
        label: 'WebM',
        mediaType: 'video',
        inputs: ['video'],
        videoCodecs: [{ id: 'vp9', label: 'VP9', encoder: 'libvpx-vp9', available: true }],
        audioCodecs: [{ id: 'opus', label: 'Opus', encoder: 'libopus', available: true, supportsBitrate: true }]
      },
      {
        id: 'gif',
        label: 'GIF',
        mediaType: 'video',
        inputs: ['video']
      },
      {
        id: 'mkv',
        label: 'MKV',
        mediaType: 'video',
        inputs: ['video'],
        audioCodecs: [
          { id: 'opus', label: 'Opus', encoder: 'libopus', available: true, supportsBitrate: true },
          { id: 'flac', label: 'FLAC', encoder: 'flac', available: true }
        ]
      },
      {
        id: 'mp3',
        label: 'MP3',
        mediaType: 'audio',
        inputs: ['video', 'audio']
      },
      {
        id: 'jpg',
        label: 'JPG',
        mediaType: 'image',
        inputs: ['video', 'image']
      }
    ]
  };
}
