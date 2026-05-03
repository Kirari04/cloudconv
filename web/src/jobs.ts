import { api } from './api';

export async function cancelJob(jobId: string, token = '') {
  const query = token ? `?token=${encodeURIComponent(token)}` : '';
  return api(`/api/jobs/${jobId}/cancel${query}`, { method: 'POST' });
}
