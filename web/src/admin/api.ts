import { api } from '../api';

export type Role = 'admin' | 'user';

export type UserRecord = {
  id: string;
  email: string;
  role: Role;
  disabled: boolean;
  createdAt: string;
  updatedAt: string;
  lastLoginAt?: string;
};

export type UploadRecord = {
  id: string;
  ownerUserId?: string;
  originalFilename: string;
  sourcePath?: string;
  mediaType?: string;
  detectedMime?: string;
  sizeBytes: number;
  bytesReceived: number;
  chunkSizeBytes: number;
  chunkCount: number;
  status: string;
  ipAddress: string;
  createdAt: string;
  updatedAt: string;
  canceledAt?: string;
  canceledByUserId?: string;
  artifactsDeletedAt?: string;
  artifactError?: string;
  adminNote?: string;
};

export type JobRecord = {
  id: string;
  uploadId: string;
  ownerUserId?: string;
  status: string;
  targetFormat: string;
  preset: string;
  optionsJson: string;
  progressPercentage: number;
  outputPath?: string;
  outputSizeBytes?: number;
  error?: string;
  startedAt?: string;
  finishedAt?: string;
  createdAt: string;
  updatedAt: string;
  removedAt?: string;
  removedByUserId?: string;
  artifactsDeletedAt?: string;
  artifactError?: string;
  adminNote?: string;
};

export type EventRecord = {
  id: number;
  level: string;
  kind: string;
  actorUserId?: string;
  uploadId?: string;
  jobId?: string;
  message: string;
  metadataJson?: string;
  createdAt: string;
};

export type PageResponse<TName extends string, T> = Record<TName, T[]> & {
  total: number;
  limit: number;
  offset: number;
};

export type AdminSummary = Record<string, number | Record<string, number>>;

export function listUsers(query: URLSearchParams) {
  return api<PageResponse<'users', UserRecord>>(`/api/admin/users?${query.toString()}`);
}

export function createUser(payload: { email: string; password: string; role: Role }) {
  return api<{ user: UserRecord }>('/api/admin/users', { method: 'POST', body: JSON.stringify(payload) });
}

export function patchUser(id: string, payload: Partial<Pick<UserRecord, 'email' | 'role' | 'disabled'>>) {
  return api<PageResponse<'users', UserRecord>>(`/api/admin/users/${id}`, { method: 'PATCH', body: JSON.stringify(payload) });
}

export function resetPassword(id: string, password?: string) {
  return api<{ password: string }>(`/api/admin/users/${id}/reset-password`, { method: 'POST', body: JSON.stringify({ password: password || '' }) });
}

export function deleteUser(id: string) {
  return api<{ status: string }>(`/api/admin/users/${id}`, { method: 'DELETE', body: JSON.stringify({}) });
}

export function listUploads(query: URLSearchParams) {
  return api<PageResponse<'uploads', UploadRecord>>(`/api/admin/uploads?${query.toString()}`);
}

export function cancelUpload(id: string, note: string) {
  return api<{ upload: UploadRecord; canceledJobIds: string[]; artifactsDeleted: boolean; artifactError?: string }>(`/api/admin/uploads/${id}/cancel`, { method: 'POST', body: JSON.stringify({ note }) });
}

export function listJobs(query: URLSearchParams) {
  return api<PageResponse<'jobs', JobRecord>>(`/api/admin/jobs?${query.toString()}`);
}

export function cancelJob(id: string, note: string) {
  return api<{ job: JobRecord }>(`/api/admin/jobs/${id}/cancel`, { method: 'POST', body: JSON.stringify({ note }) });
}

export function removeJob(id: string, note: string) {
  return api<{ job: JobRecord; artifactsDeleted: boolean; artifactError?: string }>(`/api/admin/jobs/${id}`, { method: 'DELETE', body: JSON.stringify({ note }) });
}

export function listEvents(query: URLSearchParams) {
  return api<PageResponse<'events', EventRecord>>(`/api/admin/events?${query.toString()}`);
}

export function summary() {
  return api<AdminSummary>('/api/admin/summary');
}

export function settings() {
  return api<{ settings: Record<string, string> }>('/api/admin/settings');
}

export function patchSettings(payload: Record<string, string>) {
  return api<{ settings: Record<string, string> }>('/api/admin/settings', { method: 'PATCH', body: JSON.stringify({ settings: payload }) });
}

export function queryWith(values: Record<string, string | number | boolean | undefined>) {
  const query = new URLSearchParams();
  Object.entries(values).forEach(([key, value]) => {
    if (value === undefined || value === '' || value === false) return;
    query.set(key, String(value));
  });
  return query;
}
