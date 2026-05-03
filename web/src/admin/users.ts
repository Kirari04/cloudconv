import type { SessionUser } from '../api';
import { createUser, deleteUser, listUsers, patchUser, queryWith, resetPassword, type Role, type UserRecord } from './api';
import { closeModal, dangerConfirmationValid, escapeHTML, field, filterBar, formatDateTime, getChecked, openModal, pagination, readForm, renderTable, selectInput, textInput, toast } from './components';

export type UserState = {
  limit: number;
  offset: number;
  q: string;
  role: string;
  disabled: string;
};

const roleOptions: Array<[string, string]> = [['', 'Any role'], ['admin', 'Admin'], ['user', 'User']];
const disabledOptions: Array<[string, string]> = [['', 'Any status'], ['false', 'Enabled'], ['true', 'Disabled']];

export function defaultUserState(): UserState {
  return { limit: 50, offset: 0, q: '', role: '', disabled: '' };
}

export function protectedUserAction(user: UserRecord, current: SessionUser['user'], action: 'disable' | 'demote' | 'delete') {
  if (!current || user.id !== current.id) return '';
  if (action === 'disable') return 'You cannot disable your own account.';
  if (action === 'demote') return 'You cannot demote your own account.';
  return 'You cannot delete your own account.';
}

export async function renderUsers(state: UserState, current: SessionUser['user'], rerender: () => Promise<void>) {
  const query = queryWith({ limit: state.limit, offset: state.offset, q: state.q, role: state.role, disabled: state.disabled });
  const page = await listUsers(query);
  const rows = page.users ?? [];
  rememberUsers(rows);
  return `
    <div class="space-y-6 animate-in fade-in duration-500">
      <div class="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 class="text-2xl font-black tracking-tight text-slate-900">User Management</h1>
          <p class="text-sm font-medium text-slate-500">Manage user accounts, roles, and access permissions.</p>
        </div>
        <button class="btn btn-primary shadow-lg shadow-brand-100" type="button" data-user-create>
          <i data-lucide="user-plus" class="h-4 w-4"></i>
          Create User
        </button>
      </div>

      <form data-user-filters>
        ${filterBar(`
          ${field('Search', textInput('q', state.q, 'Email or user ID'))}
          ${field('Role', selectInput('role', state.role, roleOptions))}
          ${field('Status', selectInput('disabled', state.disabled, disabledOptions))}
          <div class="flex items-end">
            <button class="btn btn-secondary w-full shadow-sm" type="submit">
              <i data-lucide="filter" class="h-4 w-4 text-slate-400"></i>
              Apply Filters
            </button>
          </div>
        `)}
      </form>

      <section class="space-y-4">
        ${renderTable<UserRecord>([
          { label: 'Email / ID', render: (u) => `
            <div class="flex flex-col">
              <span class="font-bold text-slate-900">${escapeHTML(u.email)}</span>
              <span class="text-[10px] font-mono text-slate-400 uppercase tracking-tight">${escapeHTML(u.id)}</span>
            </div>
          ` },
          { label: 'Role', render: (u) => `
            <span class="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md bg-slate-100 text-[10px] font-bold uppercase tracking-wider text-slate-600">
              <i data-lucide="${u.role === 'admin' ? 'shield' : 'user'}" class="h-3 w-3"></i>
              ${escapeHTML(u.role)}
            </span>
          ` },
          { label: 'Status', render: (u) => `
            <span class="inline-flex items-center gap-1.5 font-bold ${u.disabled ? 'text-red-500' : 'text-emerald-500'}">
              <span class="h-1.5 w-1.5 rounded-full ${u.disabled ? 'bg-red-500' : 'bg-emerald-500'}"></span>
              ${u.disabled ? 'Disabled' : 'Active'}
            </span>
          ` },
          { label: 'Last Login', render: (u) => formatDateTime(u.lastLoginAt) },
          { label: 'Actions', render: (u) => userActions(u, current), className: 'text-right' }
        ], rows)}
        ${pagination(page.total, page.limit, page.offset)}
      </section>
    </div>
  `;

  function userActions(user: UserRecord, sessionUser: SessionUser['user']) {
    const selfDisable = protectedUserAction(user, sessionUser, 'disable');
    const selfDemote = protectedUserAction(user, sessionUser, 'demote');
    const selfDelete = protectedUserAction(user, sessionUser, 'delete');
    return `
      <div class="flex justify-end gap-2">
        <button class="btn btn-ghost h-9 px-3" type="button" data-user-edit="${escapeHTML(user.id)}">
          <i data-lucide="edit-3" class="h-3.5 w-3.5"></i>
        </button>
        <button class="btn btn-ghost h-9 px-3" type="button" data-user-toggle="${escapeHTML(user.id)}" ${selfDisable ? 'disabled' : ''} title="${escapeHTML(selfDisable)}">
          <i data-lucide="${user.disabled ? 'user-check' : 'user-x'}" class="h-3.5 w-3.5"></i>
        </button>
        <button class="btn btn-ghost h-9 px-3 text-red-500 hover:bg-red-50" type="button" data-user-delete="${escapeHTML(user.id)}" ${selfDelete || selfDemote ? 'disabled' : ''} title="${escapeHTML(selfDelete || selfDemote)}">
          <i data-lucide="trash-2" class="h-3.5 w-3.5"></i>
        </button>
      </div>
    `;
  }
}

export function bindUsers(root: HTMLElement, state: UserState, current: SessionUser['user'], rerender: () => Promise<void>) {
  root.querySelector<HTMLFormElement>('[data-user-filters]')?.addEventListener('submit', async (event) => {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const data = readForm(form);
    state.q = data.q || '';
    state.role = data.role || '';
    state.disabled = data.disabled || '';
    state.offset = 0;
    await rerender();
  });
  root.querySelectorAll<HTMLButtonElement>('[data-page]').forEach((button) => {
    button.addEventListener('click', async () => {
      state.offset = button.dataset.page === 'next' ? state.offset + state.limit : Math.max(0, state.offset - state.limit);
      await rerender();
    });
  });
  root.querySelector<HTMLButtonElement>('[data-user-create]')?.addEventListener('click', () => openCreate(rerender));
  root.querySelectorAll<HTMLButtonElement>('[data-user-edit]').forEach((button) => {
    button.addEventListener('click', () => {
      const row = findUserRow(button.dataset.userEdit || '', root);
      if (row) openEdit(row, current, rerender);
    });
  });
  root.querySelectorAll<HTMLButtonElement>('[data-user-toggle]').forEach((button) => {
    button.addEventListener('click', async () => {
      const user = findUserRow(button.dataset.userToggle || '', root);
      if (!user) return;
      await patchUser(user.id, { disabled: !user.disabled });
      toast(user.disabled ? 'User enabled.' : 'User disabled.');
      await rerender();
    });
  });
  root.querySelectorAll<HTMLButtonElement>('[data-user-reset]').forEach((button) => {
    button.addEventListener('click', () => {
      const user = findUserRow(button.dataset.userReset || '', root);
      if (user) openReset(user);
    });
  });
  root.querySelectorAll<HTMLButtonElement>('[data-user-delete]').forEach((button) => {
    button.addEventListener('click', () => {
      const user = findUserRow(button.dataset.userDelete || '', root);
      if (user) openDelete(user, rerender);
    });
  });
}

let latestRows: UserRecord[] = [];

export function rememberUsers(rows: UserRecord[]) {
  latestRows = rows;
}

function findUserRow(id: string, _root: HTMLElement) {
  return latestRows.find((row) => row.id === id);
}

function openCreate(rerender: () => Promise<void>) {
  openModal('Create user', `
    <div class="grid gap-4">
      ${field('Email', textInput('email', '', 'name@example.com'))}
      ${field('Password', `<input class="field" name="password" type="password" autocomplete="new-password" placeholder="••••••••" />`)}
      ${field('Role', selectInput('role', 'user', [['user', 'User'], ['admin', 'Admin']]))}
    </div>
  `, async (form) => {
    const data = readForm(form);
    await createUser({ email: data.email, password: data.password, role: data.role as Role });
    closeModal();
    toast('User created.');
    await rerender();
  }, 'Create User');
}

function openEdit(user: UserRecord, current: SessionUser['user'], rerender: () => Promise<void>) {
  const selfDemote = protectedUserAction(user, current, 'demote');
  const selfDisable = protectedUserAction(user, current, 'disable');
  const roleSelect = selectInput('role', user.role, [['user', 'User'], ['admin', 'Admin']]).replace('<select', selfDemote ? `<select disabled title="${escapeHTML(selfDemote)}"` : '<select');
  openModal('Edit user', `
    <div class="grid gap-4">
      ${field('Email', textInput('email', user.email))}
      ${field('Role', roleSelect)}
      <label class="flex items-center gap-2.5 cursor-pointer" title="${escapeHTML(selfDisable)}">
        <input class="h-4 w-4 rounded border-slate-300 text-brand-600 focus:ring-brand-500/20" type="checkbox" name="disabled" ${user.disabled ? 'checked' : ''} ${selfDisable ? 'disabled' : ''} />
        <span class="text-sm font-bold text-slate-700">Disabled account</span>
      </label>
    </div>
  `, async (form) => {
    const data = readForm(form);
    await patchUser(user.id, { email: data.email, role: (data.role || user.role) as Role, disabled: getChecked(form, 'disabled') });
    closeModal();
    toast('User updated.');
    await rerender();
  }, 'Update User');
}

function openReset(user: UserRecord) {
  openModal('Reset password', `
    <div class="grid gap-4">
      <p class="text-sm font-medium text-slate-500 leading-relaxed">Leave the password blank to generate a secure random password for <span class="font-bold text-slate-900">${escapeHTML(user.email)}</span>.</p>
      ${field('New password', `<input class="field" name="password" type="password" autocomplete="new-password" placeholder="Optional" />`)}
    </div>
  `, async (form) => {
    const data = readForm(form);
    const result = await resetPassword(user.id, data.password);
    openModal('New password', `
      <div class="grid gap-4">
        <p class="text-sm font-medium text-slate-500">This password is shown only once. Please copy it now.</p>
        <div class="flex items-center gap-2">
          <input id="new-pwd-input" class="field font-mono text-center flex-1" readonly value="${escapeHTML(result.password)}" />
          <button class="btn btn-secondary h-10 w-10 p-0" type="button" data-copy-password="${escapeHTML(result.password)}">
            <i data-lucide="copy" class="h-4 w-4"></i>
          </button>
        </div>
      </div>
    `);
    document.querySelector<HTMLButtonElement>('[data-copy-password]')?.addEventListener('click', async (event) => {
      await navigator.clipboard.writeText((event.currentTarget as HTMLButtonElement).dataset.copyPassword || '');
      toast('Password copied.');
    });
  }, 'Confirm Reset');
}

function openDelete(user: UserRecord, rerender: () => Promise<void>) {
  openModal('Delete user', `
    <div class="grid gap-4">
      <div class="rounded-2xl bg-red-50 border border-red-100 p-5 text-red-800 text-sm font-medium leading-relaxed">
        This action is permanent. Enter the user's email <span class="font-black">${escapeHTML(user.email)}</span> below to confirm deletion.
      </div>
      ${field('Confirmation', textInput('confirm', '', 'Type email here...'))}
    </div>
  `, async (form) => {
    const data = readForm(form);
    if (!dangerConfirmationValid(user.email, data.confirm)) {
      toast('Confirmation did not match.', 'error');
      return;
    }
    await deleteUser(user.id);
    closeModal();
    toast('User deleted.');
    await rerender();
  }, 'Permanently Delete');
}
