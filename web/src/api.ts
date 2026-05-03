export type SessionUser = {
  user?: { id: string; email: string; role: 'admin' | 'user'; disabled: boolean };
  csrfToken?: string;
};

export type AppConfig = {
  catalog: {
    formats: Array<{ id: string; label: string; mediaType: string; inputs: string[] }>;
    presets: string[];
  };
  settings: Record<string, string>;
  setupNeeded: boolean;
  auth: SessionUser;
};

let csrfToken = '';

export function setCSRF(token = '') {
  csrfToken = token;
}

export function csrfHeaders(): Record<string, string> {
  return csrfToken ? { 'X-CSRF-Token': csrfToken } : {};
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  if (csrfToken && init.method && init.method !== 'GET') {
    headers.set('X-CSRF-Token', csrfToken);
  }
  const response = await fetch(path, { ...init, headers });
  const text = await response.text();
  const data = text ? JSON.parse(text) : {};
  if (!response.ok) {
    throw new Error(data.error || `Request failed with ${response.status}`);
  }
  return data as T;
}

export async function loadConfig(): Promise<AppConfig> {
  const config = await api<AppConfig>('/api/config');
  setCSRF(config.auth.csrfToken || '');
  return config;
}
