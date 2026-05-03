import { cancelUpload, listUploads, queryWith, type UploadRecord } from './api';
import { checkboxInput, closeModal, escapeHTML, field, filterBar, formatBytes, formatDateTime, openModal, pagination, readForm, renderTable, selectInput, statusBadge, toast } from './components';

export type UploadState = {
  limit: number;
  offset: number;
  q: string;
  status: string;
  mediaType: string;
  activeOnly: boolean;
};

export function defaultUploadState(): UploadState {
  return { limit: 50, offset: 0, q: '', status: '', mediaType: '', activeOnly: false };
}

export function uploadCancelAvailable(_upload: UploadRecord) {
  return true;
}

let latestRows: UploadRecord[] = [];

export async function renderUploads(state: UploadState) {
  const status = state.activeOnly ? '' : state.status;
  const query = queryWith({ limit: state.limit, offset: state.offset, q: state.q, status, mediaType: state.mediaType });
  const page = await listUploads(query);
  latestRows = state.activeOnly ? (page.uploads ?? []).filter((u) => u.status === 'uploading' || u.status === 'assembling') : (page.uploads ?? []);
  
  return `
    <div class="space-y-6 animate-in fade-in duration-500">
      <div>
        <h1 class="text-2xl font-black tracking-tight text-slate-900">Upload History</h1>
        <p class="text-sm font-medium text-slate-500">Track and manage media uploads and their current processing status.</p>
      </div>

      <form data-upload-filters>
        ${filterBar(`
          ${field('Search', `<input class="field" name="q" value="${escapeHTML(state.q)}" placeholder="File, ID, or IP..." />`)}
          ${field('Status', selectInput('status', state.status, [['', 'Any status'], ['uploading', 'Uploading'], ['assembling', 'Assembling'], ['complete', 'Complete'], ['error', 'Error'], ['canceled', 'Canceled']]))}
          ${field('Media Type', selectInput('mediaType', state.mediaType, [['', 'Any type'], ['video', 'Video'], ['audio', 'Audio'], ['image', 'Image']]))}
          <div class="flex items-end gap-4">
            <label class="flex items-center gap-2.5 cursor-pointer h-10">
              <input type="checkbox" name="activeOnly" ${state.activeOnly ? 'checked' : ''} class="h-4 w-4 rounded border-slate-300 text-brand-600 focus:ring-brand-500/20" />
              <span class="text-xs font-bold text-slate-600 uppercase tracking-tight">Active Only</span>
            </label>
            <button class="btn btn-secondary flex-1 shadow-sm" type="submit">
              <i data-lucide="filter" class="h-4 w-4 text-slate-400"></i>
              Apply Filters
            </button>
          </div>
        `)}
      </form>

      <section class="space-y-4">
        ${renderTable<UploadRecord>([
          { label: 'File / ID', render: (u) => `
            <div class="flex flex-col">
              <span class="font-bold text-slate-900 truncate max-w-xs" title="${escapeHTML(u.originalFilename)}">${escapeHTML(u.originalFilename)}</span>
              <span class="text-[10px] font-mono text-slate-400 uppercase tracking-tight">${escapeHTML(u.id)}</span>
            </div>
          ` },
          { label: 'Status', render: (u) => statusBadge(u.status) },
          { label: 'Media', render: (u) => `
            <span class="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md bg-slate-100 text-[10px] font-bold uppercase tracking-wider text-slate-600">
              <i data-lucide="${u.mediaType === 'video' ? 'video' : u.mediaType === 'audio' ? 'music' : 'image'}" class="h-3 w-3"></i>
              ${escapeHTML(u.mediaType || 'unknown')}
            </span>
          ` },
          { label: 'Progress', render: (u) => `
            <div class="flex flex-col gap-1">
              <div class="text-[10px] font-bold text-slate-500 uppercase tracking-tighter">
                ${formatBytes(u.bytesReceived)} <span class="text-slate-300">/</span> ${formatBytes(u.sizeBytes)}
              </div>
              <div class="h-1 w-24 rounded-full bg-slate-100 overflow-hidden">
                <div class="h-full bg-brand-500" style="width: ${Math.min(100, (u.bytesReceived / u.sizeBytes) * 100)}%"></div>
              </div>
            </div>
          ` },
          { label: 'IP / Owner', render: (u) => `
            <div class="flex flex-col">
              <span class="text-xs font-bold text-slate-700">${escapeHTML(u.ipAddress || 'unknown')}</span>
              <span class="text-[10px] text-slate-400 font-medium">${escapeHTML(u.ownerUserId || 'anonymous')}</span>
            </div>
          ` },
          { label: 'Created', render: (u) => formatDateTime(u.createdAt) },
          { label: 'Actions', render: uploadActions, className: 'text-right' }
        ], latestRows)}
        ${pagination(page.total, page.limit, page.offset)}
      </section>
    </div>
  `;
}

export function bindUploads(root: HTMLElement, state: UploadState, rerender: () => Promise<void>) {
  root.querySelector<HTMLFormElement>('[data-upload-filters]')?.addEventListener('submit', async (event) => {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const data = readForm(form);
    state.q = data.q || '';
    state.status = data.status || '';
    state.mediaType = data.mediaType || '';
    state.activeOnly = Boolean(form.querySelector<HTMLInputElement>('[name="activeOnly"]')?.checked);
    state.offset = 0;
    await rerender();
  });
  root.querySelectorAll<HTMLButtonElement>('[data-page]').forEach((button) => {
    button.addEventListener('click', async () => {
      state.offset = button.dataset.page === 'next' ? state.offset + state.limit : Math.max(0, state.offset - state.limit);
      await rerender();
    });
  });
  root.querySelectorAll<HTMLButtonElement>('[data-upload-cancel]').forEach((button) => {
    button.addEventListener('click', () => {
      const upload = latestRows.find((row) => row.id === button.dataset.uploadCancel);
      if (upload) openCancel(upload, rerender);
    });
  });
  root.querySelectorAll<HTMLButtonElement>('[data-upload-detail]').forEach((button) => {
    button.addEventListener('click', () => {
      const upload = latestRows.find((row) => row.id === button.dataset.uploadDetail);
      if (upload) openDetail(upload);
    });
  });
  root.querySelectorAll<HTMLButtonElement>('[data-copy]').forEach((button) => {
    button.addEventListener('click', async () => {
      await navigator.clipboard.writeText(button.dataset.copy || '');
      toast('ID copied.');
    });
  });
}

function uploadActions(upload: UploadRecord) {
  return `
    <div class="flex justify-end gap-2">
      <button class="btn btn-ghost h-9 px-3 text-slate-400 hover:text-brand-600 hover:bg-brand-50" type="button" data-upload-detail="${escapeHTML(upload.id)}" title="View Details">
        <i data-lucide="info" class="h-4 w-4"></i>
      </button>
      <button class="btn btn-ghost h-9 px-3 text-slate-400 hover:text-brand-600 hover:bg-brand-50" type="button" data-copy="${escapeHTML(upload.id)}" title="Copy Upload ID">
        <i data-lucide="copy" class="h-4 w-4"></i>
      </button>
      <button class="btn btn-ghost h-9 px-3 text-slate-400 hover:text-red-600 hover:bg-red-50" type="button" data-upload-cancel="${escapeHTML(upload.id)}" title="Cancel Upload">
        <i data-lucide="x-circle" class="h-4 w-4"></i>
      </button>
    </div>
  `;
}

function openDetail(upload: UploadRecord) {
  openModal('Upload detail', `
    <div class="grid gap-3 text-sm">
      <dl class="grid gap-2 md:grid-cols-2">
        ${detail('Upload ID', upload.id)}
        ${detail('File', upload.originalFilename)}
        ${detail('Status', upload.status)}
        ${detail('Media type', upload.mediaType || '-')}
        ${detail('Detected MIME', upload.detectedMime || '-')}
        ${detail('Received', `${formatBytes(upload.bytesReceived)} / ${formatBytes(upload.sizeBytes)}`)}
        ${detail('Owner', upload.ownerUserId || 'anonymous')}
        ${detail('IP address', upload.ipAddress || '-')}
        ${detail('Updated', formatDateTime(upload.updatedAt))}
        ${detail('Canceled', formatDateTime(upload.canceledAt))}
        ${detail('Artifact error', upload.artifactError || '-')}
        ${detail('Admin note', upload.adminNote || '-')}
      </dl>
    </div>
  `);
}

function detail(label: string, value: string) {
  return `<div><dt class="font-semibold text-slate-500">${escapeHTML(label)}</dt><dd class="break-all">${escapeHTML(value)}</dd></div>`;
}

function openCancel(upload: UploadRecord, rerender: () => Promise<void>) {
  openModal('Cancel upload', `
    <div class="grid gap-4">
      <div class="rounded-2xl bg-slate-50 border border-slate-100 p-5">
        <div class="font-bold text-slate-900">${escapeHTML(upload.originalFilename)}</div>
        <div class="mt-1 flex items-center gap-2 text-[10px] font-bold uppercase tracking-wider text-slate-400">
          <span>${escapeHTML(upload.status)}</span>
          <span class="h-1 w-1 rounded-full bg-slate-200"></span>
          <span>${formatBytes(upload.sizeBytes)}</span>
        </div>
      </div>
      <p class="text-sm font-medium text-slate-500 leading-relaxed">Queued or running jobs from this upload will be canceled. Finished job records stay visible.</p>
      ${field('Admin note', `<textarea class="field min-h-24" name="note" placeholder="Reason for cancellation..."></textarea>`)}
    </div>
  `, async (form) => {
    const data = readForm(form);
    const result = await cancelUpload(upload.id, data.note || '');
    closeModal();
    toast(`Upload canceled. ${result.canceledJobIds.length} job(s) affected.`);
    await rerender();
  }, 'Cancel Upload');
}
