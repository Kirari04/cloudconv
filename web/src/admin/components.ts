import { humanBytes } from '../catalog';

export type TableColumn<T> = {
  label: string;
  render: (row: T) => string;
  className?: string;
};

export type Tab = {
  id: string;
  label: string;
};

export function adminLayout(content: string) {
  return `
    <main class="shell mx-auto flex w-full max-w-7xl flex-col gap-8 px-6 py-10">
      <header class="flex flex-wrap items-center justify-between gap-6">
        <div class="flex items-center gap-3">
          <div class="flex h-10 w-10 items-center justify-center rounded-xl bg-slate-900 text-white shadow-lg shadow-slate-200">
            <i data-lucide="layers" class="h-6 w-6"></i>
          </div>
          <div>
            <a href="/" class="text-xl font-black tracking-tight text-slate-900">CloudConv</a>
            <p class="text-[10px] font-bold uppercase tracking-widest text-slate-400">Admin Control Center</p>
          </div>
        </div>
        <nav class="flex items-center gap-3">
          <a class="btn btn-secondary h-10" href="/">
            <i data-lucide="arrow-left" class="h-4 w-4 text-slate-400"></i>
            Back to Converter
          </a>
        </nav>
      </header>
      
      ${content}
      
      <div id="admin-modal-root"></div>
      <div id="admin-toast-root" class="fixed bottom-6 right-6 z-50 flex max-w-sm flex-col gap-3"></div>
    </main>
  `;
}

export function renderTabs(tabs: Tab[], active: string) {
  return `
    <nav class="flex gap-2 border-b border-slate-200 mb-6" aria-label="Admin sections">
      ${tabs.map((tab) => `
        <button class="px-4 py-3 text-sm font-bold transition-all border-b-2 whitespace-nowrap ${tab.id === active ? 'border-brand-600 text-brand-600' : 'border-transparent text-slate-500 hover:text-slate-700 hover:border-slate-300'}" type="button" data-tab="${tab.id}">
          ${escapeHTML(tab.label)}
        </button>
      `).join('')}
    </nav>
  `;
}

export function renderTable<T>(columns: TableColumn<T>[], rows: T[], empty = 'No records found.') {
  const body = rows.length > 0
    ? rows.map((row) => `
      <tr class="group hover:bg-slate-50/50 transition-colors border-b border-slate-100 last:border-0">
        ${columns.map((column) => `<td class="px-4 py-4 ${column.className || ''}">${column.render(row)}</td>`).join('')}
      </tr>
    `).join('')
    : `<tr><td class="px-4 py-12 text-center text-slate-400 font-medium" colspan="${columns.length}">
        <div class="flex flex-col items-center gap-2">
          <i data-lucide="search-x" class="h-8 w-8 text-slate-200"></i>
          ${escapeHTML(empty)}
        </div>
      </td></tr>`;
      
  return `
    <div class="panel overflow-hidden rounded-2xl">
      <div class="overflow-x-auto">
        <table class="w-full min-w-[56rem] text-left text-sm">
          <thead class="bg-slate-50/80 border-b border-slate-100">
            <tr>${columns.map((column) => `<th class="px-4 py-4 text-[11px] font-bold uppercase tracking-wider text-slate-500 ${column.className || ''}">${escapeHTML(column.label)}</th>`).join('')}</tr>
          </thead>
          <tbody class="divide-y divide-slate-100">${body}</tbody>
        </table>
      </div>
    </div>
  `;
}

export function metric(label: string, value: string, href?: string) {
  const inner = `
    <div class="text-[11px] font-bold uppercase tracking-widest text-slate-400 mb-2">${escapeHTML(label)}</div>
    <div class="text-3xl font-black text-slate-900 tracking-tight">${escapeHTML(value)}</div>
  `;
  if (href) {
    return `<a class="panel group relative block rounded-2xl p-6 transition-all hover:border-slate-300 hover:shadow-lg" href="${href}">${inner}</a>`;
  }
  return `<div class="panel rounded-2xl p-6">${inner}</div>`;
}

export function filterBar(content: string) {
  return `<div class="panel grid gap-4 rounded-2xl p-6 md:grid-cols-4 border-slate-200/60 shadow-sm mb-6">${content}</div>`;
}

export function field(label: string, inner: string) {
  return `
    <label class="space-y-1.5">
      <span class="text-[11px] font-bold uppercase tracking-wider text-slate-500">${escapeHTML(label)}</span>
      ${inner}
    </label>
  `;
}

export function textInput(name: string, value = '', placeholder = '') {
  return `<input class="field" name="${escapeHTML(name)}" value="${escapeHTML(value)}" placeholder="${escapeHTML(placeholder)}" />`;
}

export function selectInput(name: string, value: string, options: Array<[string, string]>) {
  return `
    <select class="field" name="${escapeHTML(name)}">
      ${options.map(([id, label]) => `<option value="${escapeHTML(id)}" ${id === value ? 'selected' : ''}>${escapeHTML(label)}</option>`).join('')}
    </select>
  `;
}

export function checkboxInput(name: string, checked: boolean, label: string) {
  return `
    <label class="flex items-center gap-2.5 cursor-pointer mt-7 h-10">
      <input type="checkbox" class="h-4 w-4 rounded border-slate-300 text-brand-600 focus:ring-brand-500/20" name="${escapeHTML(name)}" ${checked ? 'checked' : ''} />
      <span class="text-sm font-bold text-slate-700">${escapeHTML(label)}</span>
    </label>
  `;
}

export function pagination(total: number, limit: number, offset: number) {
  const from = total === 0 ? 0 : offset + 1;
  const to = Math.min(offset + limit, total);
  return `
    <div class="flex flex-wrap items-center justify-between gap-4 px-2 py-4 text-xs font-bold uppercase tracking-wider text-slate-400">
      <span>Showing ${from} - ${to} of ${total}</span>
      <div class="flex gap-2">
        <button class="btn btn-secondary h-9 px-4" type="button" data-page="prev" ${offset <= 0 ? 'disabled' : ''}>
          <i data-lucide="chevron-left" class="h-4 w-4"></i> Prev
        </button>
        <button class="btn btn-secondary h-9 px-4" type="button" data-page="next" ${offset + limit >= total ? 'disabled' : ''}>
          Next <i data-lucide="chevron-right" class="h-4 w-4"></i>
        </button>
      </div>
    </div>
  `;
}

export function statusBadge(status: string) {
  let tone = 'bg-slate-100 text-slate-700 border-slate-200';
  if (['finished', 'complete'].includes(status)) tone = 'badge-success';
  if (['converting', 'assembling', 'uploading', 'queued'].includes(status)) tone = 'badge-brand';
  if (status === 'error') tone = 'bg-red-50 text-red-700 border-red-100';
  if (status === 'canceled') tone = 'bg-slate-100 text-slate-600 border-slate-200';
  
  return `<span class="badge ${tone} uppercase tracking-tighter text-[10px]">${escapeHTML(status || 'unknown')}</span>`;
}

export function formatBytes(value: unknown) {
  return humanBytes(Number(value || 0));
}

export function formatDateTime(value: unknown) {
  if (!value) return '-';
  const date = new Date(String(value));
  if (Number.isNaN(date.getTime())) return '-';
  return `<span class="text-slate-500 tabular-nums">${date.toLocaleString()}</span>`;
}

export function shortID(value: string, length = 8) {
  return `<code class="text-[10px] font-mono font-bold text-slate-400 bg-slate-50 px-1.5 py-0.5 rounded border border-slate-100">${escapeHTML(value.slice(0, length))}</code>`;
}

export function escapeHTML(value: unknown) {
  return String(value ?? '').replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#039;' }[char] || char));
}

export function readForm(form: HTMLFormElement) {
  const data = new FormData(form);
  const out: Record<string, string> = {};
  data.forEach((value, key) => {
    out[key] = String(value);
  });
  return out;
}

export function getChecked(form: HTMLFormElement, name: string) {
  return Boolean(form.querySelector<HTMLInputElement>(`[name="${CSS.escape(name)}"]`)?.checked);
}

export function closeModal() {
  document.querySelector('#admin-modal-root')?.replaceChildren();
}

export function openModal(title: string, body: string, onSubmit?: (form: HTMLFormElement) => void | Promise<void>, submitText?: string) {
  const root = document.querySelector<HTMLElement>('#admin-modal-root');
  if (!root) return;
  
  const footer = onSubmit ? `
    <div class="flex items-center justify-end gap-3 border-t border-slate-100 bg-slate-50/50 px-8 py-5">
      <button class="btn btn-secondary" type="button" data-modal-close>Cancel</button>
      <button class="btn btn-primary shadow-lg shadow-brand-100" type="submit">${escapeHTML(submitText || 'Confirm Action')}</button>
    </div>
  ` : '';

  root.innerHTML = `
    <div class="fixed inset-0 z-50 flex items-center justify-center p-6" role="dialog" aria-modal="true">
      <div class="fixed inset-0 bg-slate-900/60 backdrop-blur-sm transition-opacity" data-modal-close></div>
      <form class="panel relative w-full max-w-xl overflow-hidden rounded-3xl bg-white shadow-2xl transition-all">
        <div class="flex items-center justify-between border-b border-slate-100 px-8 py-5">
          <h2 class="text-xl font-bold text-slate-900">${escapeHTML(title)}</h2>
          <button class="btn btn-ghost h-10 w-10 p-0 rounded-full" type="button" data-modal-close>
            <i data-lucide="x" class="h-5 w-5"></i>
          </button>
        </div>
        <div class="px-8 py-8 space-y-6">
          ${body}
        </div>
        ${footer}
      </form>
    </div>
  `;
  root.querySelectorAll('[data-modal-close]').forEach((button) => button.addEventListener('click', closeModal));
  root.querySelector('form')?.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (!onSubmit) return;
    await onSubmit(event.currentTarget as HTMLFormElement);
  });
  // Need to activate icons in the modal too
  import('lucide').then(({ createIcons, X }) => {
    createIcons({ icons: { X } });
  });
}

export function dangerConfirmationValid(expected: string, actual: string) {
  return expected.trim() !== '' && actual.trim() === expected.trim();
}

export function toast(message: string, tone: 'info' | 'error' = 'info') {
  const root = document.querySelector<HTMLElement>('#admin-toast-root');
  if (!root) return;
  const node = document.createElement('div');
  const icon = tone === 'error' ? 'alert-circle' : 'check-circle';
  const color = tone === 'error' ? 'text-red-600 bg-red-50 border-red-100 shadow-red-100' : 'text-emerald-600 bg-emerald-50 border-emerald-100 shadow-emerald-100';
  
  node.className = `panel flex items-center gap-3 rounded-2xl border px-5 py-4 text-sm font-bold shadow-xl animate-in fade-in slide-in-from-right-4 duration-300 ${color}`;
  node.innerHTML = `
    <i data-lucide="${icon}" class="h-5 w-5 shrink-0"></i>
    <p>${escapeHTML(message)}</p>
  `;
  root.appendChild(node);
  
  import('lucide').then(({ createIcons, AlertCircle, CheckCircle }) => {
    createIcons({ icons: { AlertCircle, CheckCircle }, nameAttr: 'data-lucide' });
  });

  setTimeout(() => {
    node.classList.add('animate-out', 'fade-out', 'slide-out-to-right-4');
    setTimeout(() => node.remove(), 300);
  }, 4000);
}
