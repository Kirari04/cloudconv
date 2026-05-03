import { listEvents, queryWith, type EventRecord } from './api';
import { escapeHTML, field, filterBar, formatDateTime, pagination, readForm, renderTable, selectInput, shortID } from './components';

export type EventState = {
  limit: number;
  offset: number;
  q: string;
  level: string;
  kind: string;
  jobId: string;
  uploadId: string;
  userId: string;
};

const destructiveKinds = new Set(['job.canceled', 'job.removed', 'upload.canceled', 'user.deleted', 'user.disabled', 'user.password_reset']);

export function defaultEventState(): EventState {
  return { limit: 100, offset: 0, q: '', level: '', kind: '', jobId: '', uploadId: '', userId: '' };
}

export async function renderEvents(state: EventState) {
  const query = queryWith({ limit: state.limit, offset: state.offset, q: state.q, level: state.level, kind: state.kind, jobId: state.jobId, uploadId: state.uploadId, userId: state.userId });
  const page = await listEvents(query);
  const rows = page.events ?? [];
  return `
    <div class="space-y-6 animate-in fade-in duration-500">
      <div>
        <h1 class="text-2xl font-black tracking-tight text-slate-900">System Logs</h1>
        <p class="text-sm font-medium text-slate-500">Audit trail of system activity, user actions, and background jobs.</p>
      </div>

      <form data-event-filters>
        ${filterBar(`
          ${field('Search', `<input class="field" name="q" value="${escapeHTML(state.q)}" placeholder="Message or metadata..." />`)}
          ${field('Level', selectInput('level', state.level, [['', 'Any level'], ['info', 'Info'], ['error', 'Error'], ['warn', 'Warn']]))}
          ${field('Event Kind', `<input class="field" name="kind" value="${escapeHTML(state.kind)}" placeholder="e.g. job.removed" />`)}
          <div class="flex items-end">
            <button class="btn btn-secondary w-full shadow-sm" type="submit">
              <i data-lucide="filter" class="h-4 w-4 text-slate-400"></i>
              Apply Filters
            </button>
          </div>
          ${field('Job ID', `<input class="field font-mono text-xs" name="jobId" value="${escapeHTML(state.jobId)}" />`)}
          ${field('Upload ID', `<input class="field font-mono text-xs" name="uploadId" value="${escapeHTML(state.uploadId)}" />`)}
          ${field('User ID', `<input class="field font-mono text-xs" name="userId" value="${escapeHTML(state.userId)}" />`)}
        `)}
      </form>

      <section class="space-y-4">
        ${renderTable<EventRecord>([
          { label: 'Timestamp', render: (e) => formatDateTime(e.createdAt) },
          { label: 'Level', render: (e) => `
            <span class="inline-flex items-center gap-1.5 font-bold uppercase tracking-widest text-[10px] ${e.level === 'error' ? 'text-red-600' : e.level === 'warn' ? 'text-amber-600' : 'text-slate-400'}">
              <span class="h-1.5 w-1.5 rounded-full ${e.level === 'error' ? 'bg-red-600' : e.level === 'warn' ? 'bg-amber-600' : 'bg-slate-300'}"></span>
              ${escapeHTML(e.level)}
            </span>
          ` },
          { label: 'Kind', render: (e) => kindCell(e.kind) },
          { label: 'Actor', render: (e) => e.actorUserId ? shortID(e.actorUserId) : `<span class="text-slate-300">-</span>` },
          { label: 'Context', render: (e) => `
            <div class="flex flex-col gap-1">
              ${e.jobId ? `<div class="flex items-center gap-1"><span class="text-[9px] font-black uppercase text-slate-400 w-8">Job</span> ${shortID(e.jobId)}</div>` : ''}
              ${e.uploadId ? `<div class="flex items-center gap-1"><span class="text-[9px] font-black uppercase text-slate-400 w-8">Up</span> ${shortID(e.uploadId)}</div>` : ''}
              ${!e.jobId && !e.uploadId ? `<span class="text-slate-300">-</span>` : ''}
            </div>
          ` },
          { label: 'Details', render: (e) => `
            <div class="max-w-xl">
              <div class="font-medium text-slate-700 leading-snug">${escapeHTML(e.message)}</div>
              ${e.metadataJson ? `
                <div class="mt-2 group/meta relative">
                  <pre class="max-h-32 overflow-auto rounded-xl bg-slate-50 border border-slate-100 p-3 text-[10px] font-mono text-slate-500 leading-relaxed">${escapeHTML(formatMetadata(e.metadataJson))}</pre>
                </div>
              ` : ''}
            </div>
          ` }
        ], rows)}
        ${pagination(page.total, page.limit, page.offset)}
      </section>
    </div>
  `;
}

export function bindEvents(root: HTMLElement, state: EventState, rerender: () => Promise<void>) {
  root.querySelector<HTMLFormElement>('[data-event-filters]')?.addEventListener('submit', async (event) => {
    event.preventDefault();
    const data = readForm(event.currentTarget as HTMLFormElement);
    state.q = data.q || '';
    state.level = data.level || '';
    state.kind = data.kind || '';
    state.jobId = data.jobId || '';
    state.uploadId = data.uploadId || '';
    state.userId = data.userId || '';
    state.offset = 0;
    await rerender();
  });
  root.querySelectorAll<HTMLButtonElement>('[data-page]').forEach((button) => {
    button.addEventListener('click', async () => {
      state.offset = button.dataset.page === 'next' ? state.offset + state.limit : Math.max(0, state.offset - state.limit);
      await rerender();
    });
  });
}

function kindCell(kind: string) {
  if (!destructiveKinds.has(kind)) return escapeHTML(kind);
  return `<span class="badge bg-rose-50 text-rose-800">${escapeHTML(kind)}</span>`;
}

function formatMetadata(value: string) {
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}
