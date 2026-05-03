import { api } from './api';
import { humanBytes } from './catalog';

export async function renderAdmin(root: HTMLElement) {
  root.innerHTML = layout('Loading admin data...');
  const [summary, settings, users, jobs, uploads, events] = await Promise.all([
    api<Record<string, number | Record<string, number>>>('/api/admin/summary'),
    api<{ settings: Record<string, string> }>('/api/admin/settings'),
    api<{ users: Array<{ id: string; email: string; role: string; disabled: boolean }> }>('/api/admin/users'),
    api<{ jobs: Array<Record<string, unknown>> }>('/api/admin/jobs?limit=50'),
    api<{ uploads: Array<Record<string, unknown>> }>('/api/admin/uploads?limit=50'),
    api<{ events: Array<Record<string, unknown>> }>('/api/admin/events?limit=100')
  ]);
  const userRows = users.users ?? [];
  const jobRows = jobs.jobs ?? [];
  const uploadRows = uploads.uploads ?? [];
  const eventRows = events.events ?? [];
  root.innerHTML = layout(`
    <section class="grid gap-4 md:grid-cols-5">
      ${metric('Active uploads', String(summary.activeUploads || 0))}
      ${metric('Queued', String(summary.queuedJobs || 0))}
      ${metric('Running', String(summary.convertingJobs || 0))}
      ${metric('Finished', String(summary.finishedJobs || 0))}
      ${metric('Errors', String(summary.errorJobs || 0))}
    </section>
    <section class="panel rounded-lg p-5">
      <div class="mb-4 flex items-center justify-between gap-4">
        <h2 class="text-lg font-bold">Settings</h2>
        <button id="save-settings" class="btn btn-primary" type="button">Save</button>
      </div>
      <div class="grid gap-3 md:grid-cols-2">
        ${Object.entries(settings.settings).map(([key, value]) => `
          <label class="block text-sm font-semibold text-slate-700">${key}
            <input class="field mt-1" data-setting="${key}" value="${escapeHTML(value)}" />
          </label>
        `).join('')}
      </div>
    </section>
    <section class="grid gap-4 lg:grid-cols-2">
      ${table('Users', ['Email', 'Role', 'Disabled'], userRows.map((u) => [u.email, u.role, String(u.disabled)]))}
      ${table('Recent jobs', ['ID', 'Status', 'Target'], jobRows.map((j) => [short(String(j.id)), String(j.status), String(j.targetFormat)]))}
      ${table('Uploads', ['File', 'Status', 'Size'], uploadRows.map((u) => [String(u.originalFilename), String(u.status), humanBytes(Number(u.sizeBytes || 0))]))}
      ${table('Events', ['Level', 'Kind', 'Message'], eventRows.map((e) => [String(e.level), String(e.kind), String(e.message)]))}
    </section>
  `);
  document.querySelector('#save-settings')?.addEventListener('click', async () => {
    const next: Record<string, string> = {};
    document.querySelectorAll<HTMLInputElement>('[data-setting]').forEach((input) => {
      next[input.dataset.setting || ''] = input.value;
    });
    await api('/api/admin/settings', { method: 'PATCH', body: JSON.stringify({ settings: next }) });
    await renderAdmin(root);
  });
}

function layout(content: string) {
  return `
    <main class="shell mx-auto flex w-full max-w-7xl flex-col gap-5 px-4 py-5">
      <header class="flex flex-wrap items-center justify-between gap-3">
        <a href="/" class="text-xl font-extrabold tracking-tight">CloudConv</a>
        <nav class="flex gap-2">
          <a class="btn btn-secondary" href="/">Converter</a>
          <a class="btn btn-secondary" href="/login">Login</a>
        </nav>
      </header>
      ${content}
    </main>
  `;
}

function metric(label: string, value: string) {
  return `<div class="panel rounded-lg p-4"><div class="text-sm font-semibold text-slate-500">${label}</div><div class="mt-2 text-3xl font-extrabold">${value}</div></div>`;
}

function table(title: string, headers: string[], rows: string[][]) {
  const body = rows.length > 0
    ? rows.map((row) => `<tr class="border-t border-slate-100">${row.map((cell) => `<td class="px-4 py-2">${escapeHTML(cell)}</td>`).join('')}</tr>`).join('')
    : `<tr class="border-t border-slate-100"><td class="px-4 py-4 text-slate-500" colspan="${headers.length}">No records yet.</td></tr>`;
  return `
    <section class="panel overflow-hidden rounded-lg">
      <h2 class="border-b border-slate-200 px-4 py-3 text-lg font-bold">${title}</h2>
      <div class="overflow-auto">
        <table class="w-full min-w-[32rem] text-left text-sm">
          <thead class="bg-slate-50 text-slate-600"><tr>${headers.map((h) => `<th class="px-4 py-2">${h}</th>`).join('')}</tr></thead>
          <tbody>${body}</tbody>
        </table>
      </div>
    </section>
  `;
}

function short(value: string) {
  return value.slice(0, 8);
}

function escapeHTML(value: string) {
  return value.replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#039;' }[char] || char));
}
