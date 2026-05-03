import { api, setCSRF, type SessionUser } from './api';

export async function login(email: string, password: string): Promise<SessionUser> {
  const session = await api<SessionUser>('/api/auth/login', {
    method: 'POST',
    body: JSON.stringify({ email, password })
  });
  setCSRF(session.csrfToken || '');
  return session;
}

export async function logout() {
  await api('/api/auth/logout', { method: 'POST' });
  setCSRF('');
}

export async function setup(email: string, password: string, setupToken: string) {
  return api('/api/setup', {
    method: 'POST',
    body: JSON.stringify({ email, password, setupToken })
  });
}
