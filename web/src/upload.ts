import { api, csrfHeaders } from './api';

export type UploadOptions = {
  targetFormat: string;
  preset: string;
  options: Record<string, number | boolean | string>;
};

export type UploadProgress = {
  phase: 'uploading' | 'queued' | 'converting' | 'finished' | 'error' | 'canceled';
  uploadPercent: number;
  convertPercent: number;
  message: string;
  downloadUrl?: string;
  jobId?: string;
};

export type UploadController = {
  pause(): void;
  resume(): void;
  cancel(): void;
};

export function uploadAndConvert(
  file: File,
  options: UploadOptions,
  onProgress: (progress: UploadProgress) => void
): UploadController {
  let paused = false;
  let canceled = false;
  let resume: (() => void) | undefined;
  const tokenRef = { token: '' };
  const waitIfPaused = () =>
    new Promise<void>((resolve) => {
      if (!paused) {
        resolve();
        return;
      }
      resume = resolve;
    });

  void (async () => {
    try {
      const init = await api<{ uploadId: string; chunkSizeBytes: number; chunkCount: number; token?: string }>('/api/uploads', {
        method: 'POST',
        body: JSON.stringify({ filename: file.name, size: file.size, mime: file.type })
      });
      tokenRef.token = init.token || '';
      let uploaded = 0;
      for (let index = 0; index < init.chunkCount; index += 1) {
        if (canceled) throw new Error('Upload canceled');
        await waitIfPaused();
        const start = index * init.chunkSizeBytes;
        const end = Math.min(file.size, start + init.chunkSizeBytes);
        const chunk = file.slice(start, end);
        const headers: Record<string, string> = {
          ...csrfHeaders(),
          'Content-Range': `bytes ${start}-${end - 1}/${file.size}`
        };
        if (tokenRef.token) headers['X-CloudConv-Token'] = tokenRef.token;
        await fetch(`/api/uploads/${init.uploadId}/chunks/${index}`, {
          method: 'PUT',
          headers,
          body: chunk
        }).then(async (response) => {
          if (!response.ok) {
            const payload = await response.json().catch(() => ({}));
            throw new Error(payload.error || 'Chunk upload failed');
          }
        });
        uploaded += chunk.size;
        onProgress({
          phase: 'uploading',
          uploadPercent: Math.round((uploaded / file.size) * 100),
          convertPercent: 0,
          message: 'Uploading'
        });
      }
      const query = tokenRef.token ? `?token=${encodeURIComponent(tokenRef.token)}` : '';
      const complete = await api<{ job: { id: string; status: string } }>(`/api/uploads/${init.uploadId}/complete${query}`, {
        method: 'POST',
        headers: tokenRef.token ? { 'X-CloudConv-Token': tokenRef.token } : undefined,
        body: JSON.stringify(options)
      });
      await pollJob(complete.job.id, tokenRef.token, onProgress);
    } catch (error) {
      onProgress({
        phase: canceled ? 'canceled' : 'error',
        uploadPercent: 0,
        convertPercent: 0,
        message: error instanceof Error ? error.message : 'Upload failed'
      });
    }
  })();

  return {
    pause() {
      paused = true;
    },
    resume() {
      paused = false;
      resume?.();
    },
    cancel() {
      canceled = true;
      resume?.();
    }
  };
}

async function pollJob(jobId: string, token: string, onProgress: (progress: UploadProgress) => void) {
  const query = token ? `?token=${encodeURIComponent(token)}` : '';
  for (;;) {
    const job = await api<{
      id: string;
      status: UploadProgress['phase'];
      queuePosition?: number;
      progressPercentage?: number;
      downloadUrl?: string;
      error?: string;
    }>(`/api/jobs/${jobId}${query}`);
    if (job.status === 'finished') {
      onProgress({
        phase: 'finished',
        uploadPercent: 100,
        convertPercent: 100,
        message: 'Finished',
        downloadUrl: job.downloadUrl,
        jobId
      });
      return;
    }
    if (job.status === 'error' || job.status === 'canceled') {
      onProgress({
        phase: job.status,
        uploadPercent: 100,
        convertPercent: job.progressPercentage || 0,
        message: job.error || job.status,
        jobId
      });
      return;
    }
    onProgress({
      phase: job.status,
      uploadPercent: 100,
      convertPercent: job.progressPercentage || 0,
      message: job.status === 'queued' ? `Queued ${job.queuePosition || ''}` : 'Converting',
      jobId
    });
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
}
