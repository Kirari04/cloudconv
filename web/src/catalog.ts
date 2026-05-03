export type MediaType = 'video' | 'audio' | 'image';

export type CodecOption = {
  id: string;
  label: string;
  encoder: string;
  available: boolean;
  recommended?: boolean;
  supportsBitrate?: boolean;
};

export type FormatOption = {
  id: string;
  label: string;
  mediaType: string;
  inputs: string[];
  videoCodecs?: CodecOption[];
  audioCodecs?: CodecOption[];
};

export type PresetDetailItem = {
  label: string;
  value: string;
};

export type PresetEffect = {
  summary: string;
  details: PresetDetailItem[];
  values: Record<string, number | boolean | string>;
};

export type PresetEffectKey = 'video' | 'gif' | 'image' | 'audio';

export type PresetDetail = {
  id: string;
  label: string;
  summary: string;
  effects: Record<PresetEffectKey, PresetEffect>;
};

export type Catalog = {
  formats: FormatOption[];
  presets: string[];
  presetDetails?: PresetDetail[];
};

export type PresetSummaryRow = {
  label: string;
  value: string;
};

const imageExt = new Set(['jpg', 'jpeg', 'png', 'webp', 'bmp', 'tiff']);
const videoExt = new Set(['mp4', 'webm', 'mov', 'avi', 'mkv', 'gif']);
const audioExt = new Set(['mp3', 'wav', 'ogg', 'flac']);

export function extension(filename: string): string {
  const parts = filename.toLowerCase().split('.');
  return parts.length > 1 ? parts.pop() || '' : '';
}

export function detectMediaType(file: File): MediaType | 'unknown' {
  if (file.type.startsWith('video/')) return 'video';
  if (file.type.startsWith('audio/')) return 'audio';
  if (file.type.startsWith('image/') && extension(file.name) !== 'gif') return 'image';
  const ext = extension(file.name);
  if (imageExt.has(ext)) return 'image';
  if (videoExt.has(ext)) return 'video';
  if (audioExt.has(ext)) return 'audio';
  return 'unknown';
}

export function formatsFor(catalog: { formats: FormatOption[] }, type: MediaType) {
  return catalog.formats.filter((format) => format.inputs.includes(type));
}

export function formatById(catalog: { formats: FormatOption[] }, id: string) {
  return catalog.formats.find((format) => format.id === id);
}

export function presetById(catalog: Partial<Catalog>, presetId: string) {
  return catalog.presetDetails?.find((preset) => preset.id === presetId) || fallbackPresetDetails.find((preset) => preset.id === presetId);
}

export function presetEffectFor(catalog: Partial<Catalog>, presetId: string, type: MediaType, formatId: string) {
  const preset = presetById(catalog, presetId);
  const key: PresetEffectKey = type === 'video' && formatId === 'gif' ? 'gif' : type;
  return preset?.effects?.[key];
}

export function recommendedCodecLabel(format: FormatOption | undefined, kind: 'video' | 'audio', selectedId = '') {
  const codecs = kind === 'video' ? format?.videoCodecs : format?.audioCodecs;
  if (!codecs?.length) return '';
  const selected = selectedId ? codecs.find((codec) => codec.id === selectedId) : undefined;
  const choice = selected || codecs.find((codec) => codec.recommended) || codecs[0];
  return choice?.label || '';
}

export function presetSummaryRows(
  type: MediaType,
  options: Record<string, number | boolean | string>,
  format: FormatOption | undefined,
  effect: PresetEffect | undefined
): PresetSummaryRow[] {
  if (type === 'video' && format?.id === 'gif') {
    const defaultWidth = numberValue(effect, 'maxWidth');
    const defaultFPS = numberValue(effect, 'framerate');
    const width = numberOption(options, 'maxWidth');
    const fps = numberOption(options, 'framerate');
    return [
      { label: 'Output', value: 'GIF' },
      { label: 'Width', value: width ? `Overridden to ${width}px` : defaultWidth ? `${defaultWidth}px` : 'Auto' },
      { label: 'FPS', value: fps ? `Overridden to ${fps} FPS` : defaultFPS ? `${defaultFPS} FPS` : 'Auto' },
      { label: 'Loop', value: (options.loop ?? true) ? 'On' : 'Off' }
    ];
  }

  if (type === 'video') {
    const defaultHeight = numberValue(effect, 'maxHeight');
    const height = numberOption(options, 'maxHeight');
    const fps = numberOption(options, 'framerate');
    return [
      { label: 'Output', value: format?.label || 'Video' },
      { label: 'Max height', value: height ? `Overridden to ${height}p` : defaultHeight ? `${defaultHeight}p` : 'Auto' },
      { label: 'Video codec', value: codecSummary(format, 'video', String(options.videoCodec || '')) },
      { label: 'Audio codec', value: codecSummary(format, 'audio', String(options.audioCodec || '')) },
      { label: 'FPS', value: fps ? `Overridden to ${fps} FPS` : 'Original' }
    ];
  }

  if (type === 'image') {
    const defaultQuality = numberValue(effect, 'quality');
    const quality = numberOption(options, 'quality');
    const width = numberOption(options, 'maxWidth');
    return [
      { label: 'Output', value: format?.label || 'Image' },
      { label: 'Quality', value: quality ? `Overridden to ${quality}%` : defaultQuality ? `${defaultQuality}%` : 'Auto' },
      { label: 'Max width', value: width ? `Limited to ${width}px` : 'Original' }
    ];
  }

  const audioBitrate = numberOption(options, 'audioBitrate');
  return [
    { label: 'Output', value: format?.label || 'Audio' },
    { label: 'Preset effect', value: 'No audio changes' },
    { label: 'Audio bitrate', value: audioBitrate ? `Overridden to ${audioBitrate} kbps` : 'Auto unless changed' }
  ];
}

export function presetOptionLabel(catalog: Partial<Catalog>, presetId: string, type: MediaType, formatId: string) {
  const preset = presetById(catalog, presetId);
  const effect = presetEffectFor(catalog, presetId, type, formatId);
  const label = preset?.label || titleCase(presetId);
  if (!effect) return label;
  if (type === 'video' && formatId === 'gif') {
    const width = numberValue(effect, 'maxWidth');
    const fps = numberValue(effect, 'framerate');
    return width && fps ? `${label} - ${width}px / ${fps} FPS` : label;
  }
  if (type === 'video') {
    const height = numberValue(effect, 'maxHeight');
    return height ? `${label} - max ${height}p` : label;
  }
  if (type === 'image') {
    const quality = numberValue(effect, 'quality');
    return quality ? `${label} - ${quality}% quality` : label;
  }
  return `${label} - no preset audio changes`;
}

export function presetPlaceholder(catalog: Partial<Catalog>, presetId: string, type: MediaType, formatId: string, key: string, fallback: string) {
  const effect = presetEffectFor(catalog, presetId, type, formatId);
  const value = numberValue(effect, key);
  return value ? String(value) : fallback;
}

export function resetInvalidCodecOptions(options: Record<string, number | boolean | string>, format?: FormatOption) {
  if (format?.id === 'gif') {
    options.videoCodec = '';
    options.audioCodec = '';
    delete options.videoBitrate;
    delete options.audioBitrate;
    return;
  }
  if (!format?.videoCodecs?.some((codec) => codec.id === options.videoCodec)) {
    options.videoCodec = '';
  }
  if (!format?.audioCodecs?.some((codec) => codec.id === options.audioCodec)) {
    options.audioCodec = '';
  }
  if (!audioCodecSupportsBitrate(format, String(options.audioCodec || ''))) {
    delete options.audioBitrate;
  }
}

export function audioCodecSupportsBitrate(format: FormatOption | undefined, audioCodecId: string) {
  if (!format?.audioCodecs?.length) return true;
  const choice = audioCodecId
    ? format.audioCodecs.find((codec) => codec.id === audioCodecId)
    : format.audioCodecs.find((codec) => codec.recommended) || format.audioCodecs[0];
  return choice?.supportsBitrate === true;
}

function codecSummary(format: FormatOption | undefined, kind: 'video' | 'audio', selectedId: string) {
  const label = recommendedCodecLabel(format, kind, selectedId);
  if (!label) return 'Auto';
  return selectedId ? label : `Auto (${label})`;
}

function numberOption(options: Record<string, number | boolean | string>, key: string) {
  const value = options[key];
  return typeof value === 'number' && value > 0 ? value : 0;
}

function numberValue(effect: PresetEffect | undefined, key: string) {
  const value = effect?.values?.[key];
  return typeof value === 'number' ? value : 0;
}

function titleCase(value: string) {
  return value.slice(0, 1).toUpperCase() + value.slice(1);
}

const fallbackPresetDetails: PresetDetail[] = [
  presetDetail('small', 'Small', 'Smaller output with lower visual detail.', 480, 320, 15, 72),
  presetDetail('balanced', 'Balanced', 'Good default quality with moderate output size.', 720, 480, 15, 86),
  presetDetail('high', 'High', 'Higher quality output with larger files.', 1080, 720, 20, 95)
];

function presetDetail(id: string, label: string, summary: string, videoHeight: number, gifWidth: number, gifFPS: number, imageQuality: number): PresetDetail {
  return {
    id,
    label,
    summary,
    effects: {
      video: {
        summary: `Limits video height to ${videoHeight}p unless overridden.`,
        details: [{ label: 'Max height', value: `${videoHeight}p` }],
        values: { maxHeight: videoHeight }
      },
      gif: {
        summary: `Creates a ${gifWidth}px wide GIF at ${gifFPS} FPS.`,
        details: [{ label: 'Width', value: `${gifWidth}px` }],
        values: { maxWidth: gifWidth, framerate: gifFPS, loop: true }
      },
      image: {
        summary: `Uses ${imageQuality}% image quality unless overridden.`,
        details: [{ label: 'Quality', value: `${imageQuality}%` }],
        values: { quality: imageQuality }
      },
      audio: {
        summary: 'Does not change audio settings by itself.',
        details: [{ label: 'Preset effect', value: 'No audio changes' }],
        values: {}
      }
    }
  };
}

export function humanBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = bytes / 1024;
  let index = 0;
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024;
    index += 1;
  }
  return `${value.toFixed(value >= 10 ? 0 : 1)} ${units[index]}`;
}
