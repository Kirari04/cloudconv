export type MediaType = 'video' | 'audio' | 'image';

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

export function formatsFor(catalog: { formats: Array<{ id: string; label: string; inputs: string[] }> }, type: MediaType) {
  return catalog.formats.filter((format) => format.inputs.includes(type));
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
