import { describe, expect, it } from 'vitest';
import { dangerConfirmationValid } from './components';
import { jobCancelAvailable, jobRemoveAvailable } from './jobs';
import { uploadCancelAvailable } from './uploads';
import { protectedUserAction } from './users';
import type { UploadRecord, UserRecord } from './api';

describe('admin action availability', () => {
  it('validates destructive confirmations exactly', () => {
    expect(dangerConfirmationValid('abc12345', 'abc12345')).toBe(true);
    expect(dangerConfirmationValid('abc12345', 'abc1234')).toBe(false);
  });

  it('allows expected job actions by status', () => {
    expect(jobCancelAvailable({ status: 'converting' })).toBe(true);
    expect(jobRemoveAvailable({ status: 'converting' })).toBe(true);
    expect(jobCancelAvailable({ status: 'removed' })).toBe(false);
    expect(jobRemoveAvailable({ status: 'removed' })).toBe(false);
  });

  it('allows upload cancel for active uploads', () => {
    expect(uploadCancelAvailable({ status: 'uploading' } as UploadRecord)).toBe(true);
  });

  it('protects self user actions', () => {
    const current = { id: 'u1', email: 'admin@example.com', role: 'admin' as const, disabled: false };
    const user = { ...current, createdAt: '', updatedAt: '' } as UserRecord;
    expect(protectedUserAction(user, current, 'disable')).toContain('disable');
    expect(protectedUserAction(user, current, 'delete')).toContain('delete');
  });
});
