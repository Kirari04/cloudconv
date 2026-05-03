import { cancelJob, listJobs, queryWith, removeJob, type JobRecord } from './api';
import { checkboxInput, closeModal, dangerConfirmationValid, escapeHTML, field, filterBar, formatBytes, formatDateTime, openModal, pagination, readForm, renderTable, selectInput, shortID, statusBadge, toast } from './components';

export type JobState = {
  limit: number;
  offset: number;
  q: string;
  status: string;
  targetFormat: string;
  includeRemoved: boolean;
};

export function defaultJobState(): JobState {
  return { limit: 50, offset: 0, q: '', status: '', targetFormat: '', includeRemoved: false };
}

export function jobCancelAvailable(job: Pick<JobRecord, 'status'>) {
  return job.status === 'queued' || job.status === 'converting';
}

export function jobRemoveAvailable(job: Pick<JobRecord, 'status'>) {
  return job.status !== 'removed';
}

let latestRows: JobRecord[] = [];

export async function renderJobs(state: JobState) {
  const query = queryWith({ limit: state.limit, offset: state.offset, q: state.q, status: state.status, targetFormat: state.targetFormat, includeRemoved: state.includeRemoved });
  const page = await listJobs(query);
  latestRows = page.jobs ?? [];
  
  return `
    <div class="space-y-6 animate-in fade-in duration-500">
      <div>
        <h1 class="text-2xl font-black tracking-tight text-slate-900">Conversion Jobs</h1>
        <p class="text-sm font-medium text-slate-500">Monitor and manage background media processing tasks.</p>
      </div>

      <form data-job-filters>
        ${filterBar(`
          ${field('Search', `<input class="field" name="q" value="${escapeHTML(state.q)}" placeholder="Job ID, upload ID..." />`)}
          ${field('Status', selectInput('status', state.status, [['', 'Any status'], ['queued', 'Queued'], ['converting', 'Converting'], ['finished', 'Finished'], ['error', 'Error'], ['canceled', 'Canceled'], ['removed', 'Removed']]))}
          ${field('Target Format', `<input class="field" name="targetFormat" value="${escapeHTML(state.targetFormat)}" placeholder="e.g. mp4, webp" />`)}
          <div class="flex items-end gap-4">
            <label class="flex items-center gap-2.5 cursor-pointer h-10">
              <input type="checkbox" name="includeRemoved" ${state.includeRemoved ? 'checked' : ''} class="h-4 w-4 rounded border-slate-300 text-brand-600 focus:ring-brand-500/20" />
              <span class="text-xs font-bold text-slate-600 uppercase tracking-tight">Show Removed</span>
            </label>
            <button class="btn btn-secondary flex-1 shadow-sm" type="submit">
              <i data-lucide="filter" class="h-4 w-4 text-slate-400"></i>
              Apply Filters
            </button>
          </div>
        `)}
      </form>

      <section class="space-y-4">
        ${renderTable<JobRecord>([
          { label: 'Job / ID', render: (j) => `
            <div class="flex flex-col">
              <span class="font-mono text-xs font-bold text-slate-900 uppercase tracking-tighter">${shortID(j.id, 12)}</span>
              <span class="text-[10px] font-mono text-slate-400 uppercase tracking-tight">${escapeHTML(j.id.slice(12))}</span>
            </div>
          ` },
          { label: 'Status', render: (j) => statusBadge(j.status) },
          { label: 'Task', render: (j) => `
            <div class="flex items-center gap-2">
              <span class="inline-flex h-6 w-6 items-center justify-center rounded bg-slate-100 text-[10px] font-black uppercase text-slate-600 border border-slate-200">
                ${escapeHTML(j.targetFormat)}
              </span>
              <span class="text-[10px] font-bold text-slate-400 uppercase tracking-widest">${escapeHTML(j.preset)}</span>
            </div>
          ` },
          { label: 'Progress', render: (j) => `
            <div class="flex flex-col gap-1 w-28">
              <div class="text-[10px] font-bold text-slate-500 uppercase tracking-tighter">
                ${Number(j.progressPercentage || 0)}%
              </div>
              <div class="h-1 rounded-full bg-slate-100 overflow-hidden">
                <div class="h-full bg-brand-500 transition-all duration-500" style="width: ${j.progressPercentage || 0}%"></div>
              </div>
            </div>
          ` },
          { label: 'Source', render: (j) => `
            <button class="group flex items-center gap-1.5 font-mono text-[10px] font-bold text-emerald-700 hover:text-emerald-800 transition-colors" type="button" data-copy="${escapeHTML(j.uploadId)}" title="Click to copy Upload ID">
              <i data-lucide="external-link" class="h-3 w-3 text-emerald-400 group-hover:scale-110"></i>
              ${shortID(j.uploadId)}
            </button>
          ` },
          { label: 'Output', render: (j) => j.outputSizeBytes ? `
            <span class="text-xs font-bold text-slate-700 tabular-nums">${formatBytes(j.outputSizeBytes)}</span>
          ` : `<span class="text-slate-300">-</span>` },
          { label: 'Created', render: (j) => formatDateTime(j.createdAt) },
          { label: 'Actions', render: jobActions, className: 'text-right' }
        ], latestRows)}
        ${pagination(page.total, page.limit, page.offset)}
      </section>
    </div>
  `;
}

export function bindJobs(root: HTMLElement, state: JobState, rerender: () => Promise<void>) {
  root.querySelector<HTMLFormElement>('[data-job-filters]')?.addEventListener('submit', async (event) => {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const data = readForm(form);
    state.q = data.q || '';
    state.status = data.status || '';
    state.targetFormat = data.targetFormat || '';
    state.includeRemoved = Boolean(form.querySelector<HTMLInputElement>('[name="includeRemoved"]')?.checked);
    state.offset = 0;
    await rerender();
  });
  root.querySelectorAll<HTMLButtonElement>('[data-page]').forEach((button) => {
    button.addEventListener('click', async () => {
      state.offset = button.dataset.page === 'next' ? state.offset + state.limit : Math.max(0, state.offset - state.limit);
      await rerender();
    });
  });
  root.querySelectorAll<HTMLButtonElement>('[data-job-cancel]').forEach((button) => {
    button.addEventListener('click', () => {
      const job = latestRows.find((row) => row.id === button.dataset.jobCancel);
      if (job) openCancel(job, rerender);
    });
  });
  root.querySelectorAll<HTMLButtonElement>('[data-job-remove]').forEach((button) => {
    button.addEventListener('click', () => {
      const job = latestRows.find((row) => row.id === button.dataset.jobRemove);
      if (job) openRemove(job, rerender);
    });
  });
  root.querySelectorAll<HTMLButtonElement>('[data-job-detail]').forEach((button) => {
    button.addEventListener('click', () => {
      const job = latestRows.find((row) => row.id === button.dataset.jobDetail);
      if (job) openDetail(job);
    });
  });
  root.querySelectorAll<HTMLButtonElement>('[data-copy]').forEach((button) => {
    button.addEventListener('click', async () => {
      await navigator.clipboard.writeText(button.dataset.copy || '');
      toast('ID copied.');
    });
  });
}

function jobActions(job: JobRecord) {
  const canCancel = jobCancelAvailable(job);
  const canRemove = jobRemoveAvailable(job);
  
  return `
    <div class="flex justify-end gap-2">
      <button class="btn btn-ghost h-9 px-3 text-slate-400 hover:text-brand-600 hover:bg-brand-50" type="button" data-job-detail="${escapeHTML(job.id)}" title="View Details">
        <i data-lucide="info" class="h-4 w-4"></i>
      </button>
      <button class="btn btn-ghost h-9 px-3 text-slate-400 hover:text-brand-600 hover:bg-brand-50" type="button" data-job-cancel="${escapeHTML(job.id)}" ${canCancel ? '' : 'disabled'} title="Cancel Job">
        <i data-lucide="pause-circle" class="h-4 w-4"></i>
      </button>
      <button class="btn btn-ghost h-9 px-3 text-slate-400 hover:text-red-600 hover:bg-red-50" type="button" data-job-remove="${escapeHTML(job.id)}" ${canRemove ? '' : 'disabled'} title="Remove Job">
        <i data-lucide="trash-2" class="h-4 w-4"></i>
      </button>
    </div>
  `;
}

function openCancel(job: JobRecord, rerender: () => Promise<void>) {
  openModal('Cancel job', `
    <div class="grid gap-4">
      <div class="rounded-2xl bg-slate-50 border border-slate-100 p-5">
        <div class="font-mono font-bold text-slate-900">${escapeHTML(job.id)}</div>
        <div class="mt-1 flex items-center gap-2 text-[10px] font-bold uppercase tracking-wider text-slate-400">
          <span>${escapeHTML(job.status)}</span>
          <span class="h-1 w-1 rounded-full bg-slate-200"></span>
          <span>Target: ${escapeHTML(job.targetFormat)}</span>
        </div>
      </div>
      <p class="text-sm font-medium text-slate-500 leading-relaxed">The conversion process will be stopped immediately. This cannot be undone.</p>
      ${field('Admin note', `<textarea class="field min-h-24" name="note" placeholder="Reason for cancellation..."></textarea>`)}
    </div>
  `, async (form) => {
    const data = readForm(form);
    await cancelJob(job.id, data.note || '');
    closeModal();
    toast('Job canceled.');
    await rerender();
  }, 'Cancel Job');
}

function openRemove(job: JobRecord, rerender: () => Promise<void>) {
  const confirmation = job.id.slice(0, 8);
  openModal('Remove job', `
    <div class="grid gap-4">
      <div class="rounded-2xl bg-red-50 border border-red-100 p-5 text-red-800 text-sm font-medium leading-relaxed">
        ${job.status === 'converting' ? 'FFmpeg will be stopped before artifacts are removed.' : 'This will delete the job record and all associated artifacts from the server.'}
      </div>
      <div class="font-mono text-xs text-slate-400 px-2 uppercase tracking-tighter">Confirm job removal</div>
      ${field(`Type first 8 chars: ${confirmation}`, `<input class="field font-mono" name="confirm" placeholder="${confirmation}" />`)}
      ${field('Admin note', `<textarea class="field min-h-24" name="note" placeholder="Reason for removal..."></textarea>`)}
    </div>
  `, async (form) => {
    const data = readForm(form);
    if (!dangerConfirmationValid(confirmation, data.confirm)) {
      toast('Confirmation did not match.', 'error');
      return;
    }
    await removeJob(job.id, data.note || '');
    closeModal();
    toast('Job removed.');
    await rerender();
  }, 'Permanently Remove');
}

function openDetail(job: JobRecord) {
  const parsedOptions = parseJSONObject(job.optionsJson);
  const options = safeJSON(job.optionsJson);
  const effectiveRows = [
    typeof parsedOptions.effectiveVideoEncoder === 'string' && parsedOptions.effectiveVideoEncoder
      ? detail('Video encoder', parsedOptions.effectiveVideoEncoder)
      : '',
    typeof parsedOptions.effectiveAudioEncoder === 'string' && parsedOptions.effectiveAudioEncoder
      ? detail('Audio encoder', parsedOptions.effectiveAudioEncoder)
      : ''
  ].join('');
  openModal('Job detail', `
    <div class="grid gap-3 text-sm">
      <dl class="grid gap-2 md:grid-cols-2">
        ${detail('Job ID', job.id)}
        ${detail('Upload ID', job.uploadId)}
        ${detail('Status', job.status)}
        ${detail('Target', job.targetFormat)}
        ${detail('Created', formatDateTime(job.createdAt))}
        ${detail('Finished', formatDateTime(job.finishedAt))}
        ${detail('Output path', job.outputPath || '-')}
        ${detail('Artifact error', job.artifactError || '-')}
        ${effectiveRows}
      </dl>
      <div>
        <div class="mb-1 font-semibold">Options</div>
        <pre class="max-h-64 overflow-auto rounded-md bg-slate-950 p-3 text-xs text-white">${escapeHTML(options)}</pre>
      </div>
      ${job.error ? `<div class="rounded-md bg-rose-50 p-3 text-rose-800">${escapeHTML(job.error)}</div>` : ''}
    </div>
  `);
}

function detail(label: string, value: string) {
  return `<div><dt class="font-semibold text-slate-500">${escapeHTML(label)}</dt><dd class="break-all">${escapeHTML(value)}</dd></div>`;
}

function safeJSON(value: string) {
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value || '{}';
  }
}

function parseJSONObject(value: string): Record<string, unknown> {
  try {
    const parsed = JSON.parse(value);
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {};
  } catch {
    return {};
  }
}
