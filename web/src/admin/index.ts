import { activateIcons } from '../main';
import { humanBytes } from '../catalog';
import type { SessionUser } from '../api';
import { listEvents, listJobs, queryWith, summary } from './api';
import { adminLayout, escapeHTML, metric, renderTabs, shortID, statusBadge, toast } from './components';
import { bindEvents, defaultEventState, renderEvents } from './events';
import { bindJobs, defaultJobState, renderJobs } from './jobs';
import { bindSettings, renderSettings } from './settings';
import { bindUploads, defaultUploadState, renderUploads } from './uploads';
import { bindUsers, defaultUserState, renderUsers } from './users';

type AdminTab = 'overview' | 'users' | 'uploads' | 'jobs' | 'events' | 'settings';

const tabs: Array<{ id: AdminTab; label: string }> = [
  { id: 'overview', label: 'Overview' },
  { id: 'users', label: 'Users' },
  { id: 'uploads', label: 'Uploads' },
  { id: 'jobs', label: 'Jobs' },
  { id: 'events', label: 'Events' },
  { id: 'settings', label: 'Settings' }
];

const state = {
  tab: 'overview' as AdminTab,
  users: defaultUserState(),
  uploads: defaultUploadState(),
  jobs: defaultJobState(),
  events: defaultEventState()
};

export async function renderAdmin(root: HTMLElement, session?: SessionUser) {
  const current = session?.user;
  async function rerender() {
    try {
      root.innerHTML = adminLayout(renderTabs(tabs, state.tab) + `<div id="admin-panel">${await tabContent(current)}</div>`);
      bindRoot(root, current, rerender);
      activateIcons();
    } catch (error) {
      root.innerHTML = adminLayout(`<section class="panel rounded-lg p-5 text-rose-800">${error instanceof Error ? error.message : 'Could not load admin data.'}</section>`);
    }
  }
  root.innerHTML = adminLayout('Loading admin data...');
  await rerender();
}

async function tabContent(current: SessionUser['user']) {
  switch (state.tab) {
    case 'users':
      return renderUsers(state.users, current, async () => undefined);
    case 'uploads':
      return renderUploads(state.uploads);
    case 'jobs':
      return renderJobs(state.jobs);
    case 'events':
      return renderEvents(state.events);
    case 'settings':
      return renderSettings();
    default:
      return renderOverview();
  }
}

function bindRoot(root: HTMLElement, current: SessionUser['user'], rerender: () => Promise<void>) {
  root.querySelectorAll<HTMLButtonElement>('[data-tab]').forEach((button) => {
    button.addEventListener('click', async () => {
      state.tab = button.dataset.tab as AdminTab;
      await rerender();
    });
  });
  const wrapped = async () => {
    try {
      await rerender();
    } catch (error) {
      toast(error instanceof Error ? error.message : 'Request failed.', 'error');
    }
  };
  if (state.tab === 'users') bindUsers(root, state.users, current, wrapped);
  if (state.tab === 'uploads') bindUploads(root, state.uploads, wrapped);
  if (state.tab === 'jobs') bindJobs(root, state.jobs, wrapped);
  if (state.tab === 'events') bindEvents(root, state.events, wrapped);
  if (state.tab === 'settings') bindSettings(root, wrapped);
  if (state.tab === 'overview') {
    root.querySelectorAll<HTMLButtonElement>('[data-overview-tab]').forEach((button) => {
      button.addEventListener('click', async () => {
        state.tab = button.dataset.overviewTab as AdminTab;
        if (state.tab === 'uploads') {
          state.uploads.status = button.dataset.status || '';
          state.uploads.activeOnly = button.dataset.status === 'uploading';
        }
        if (state.tab === 'jobs') {
          state.jobs.status = button.dataset.status || '';
        }
        if (state.tab === 'users') {
          state.users.disabled = button.dataset.disabled || '';
        }
        if (state.tab === 'events') {
          state.events.kind = button.dataset.kind || '';
        }
        await rerender();
      });
    });
  }
}

async function renderOverview() {
  const [summaryData, recentJobs, recentEvents] = await Promise.all([
    summary(),
    listJobs(queryWith({ limit: 5 })),
    listEvents(queryWith({ limit: 8 }))
  ]);

  const data = summaryData as Record<string, any>;
  const disk = (data.disk || {}) as Record<string, number>;
  const totalProcessed = Number(data.bytesProcessed || 0);

  return `
    <div class="space-y-8 animate-in fade-in duration-500">
      <div class="flex flex-wrap items-center justify-between gap-6">
        <div>
          <h1 class="text-3xl font-black tracking-tight text-slate-900">System Overview</h1>
          <p class="text-sm font-medium text-slate-500 text-balance">Real-time health monitoring and activity feed for the media engine.</p>
        </div>
        <div class="flex flex-wrap gap-2">
          <button class="btn btn-secondary shadow-sm" type="button" data-overview-tab="uploads" data-status="uploading">
            <i data-lucide="upload-cloud" class="h-4 w-4 text-slate-400"></i>
            Active Uploads (${Number(data.activeUploads || 0)})
          </button>
          <button class="btn btn-primary shadow-lg shadow-brand-100" type="button" data-overview-tab="jobs" data-status="queued">
            <i data-lucide="clock" class="h-4 w-4"></i>
            Manage Queue (${Number(data.queuedJobs || 0)})
          </button>
        </div>
      </div>

      <div class="grid gap-8 lg:grid-cols-[1fr_24rem]">
        <div class="space-y-8">
          <!-- Primary Metrics -->
          <section class="grid gap-6 sm:grid-cols-2">
            <div class="panel rounded-3xl p-8 border-slate-200/60 flex flex-col justify-between">
              <div class="flex items-center gap-3 mb-6">
                <div class="flex h-10 w-10 items-center justify-center rounded-xl bg-emerald-50 text-emerald-600">
                  <i data-lucide="activity" class="h-5 w-5"></i>
                </div>
                <h2 class="text-sm font-bold uppercase tracking-widest text-slate-400">Total Volume</h2>
              </div>
              <div>
                <div class="text-4xl font-black text-slate-900 tabular-nums mb-1">${humanBytes(totalProcessed)}</div>
                <p class="text-xs font-bold text-slate-400 uppercase tracking-tight">Successfully processed to date</p>
              </div>
            </div>

            <div class="panel rounded-3xl p-8 border-slate-200/60">
              <div class="flex items-center gap-3 mb-6">
                <div class="flex h-10 w-10 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
                  <i data-lucide="layers" class="h-5 w-5"></i>
                </div>
                <h2 class="text-sm font-bold uppercase tracking-widest text-slate-400">Status Distribution</h2>
              </div>
              <div class="space-y-4">
                ${statusRow('Running', Number(data.convertingJobs || 0), 'bg-brand-500')}
                ${statusRow('Queued', Number(data.queuedJobs || 0), 'bg-amber-400')}
                ${statusRow('Failed', Number(data.errorJobs || 0), 'bg-red-500')}
              </div>
            </div>
          </section>

          <!-- System Health -->
          <section class="panel rounded-3xl p-8 border-slate-200/60 shadow-sm">
            <div class="flex items-center gap-3 mb-8">
              <div class="flex h-10 w-10 items-center justify-center rounded-xl bg-slate-900 text-white">
                <i data-lucide="hard-drive" class="h-5 w-5"></i>
              </div>
              <div>
                <h2 class="text-xl font-bold text-slate-900">Storage Health</h2>
                <p class="text-xs font-bold text-slate-400 uppercase tracking-widest">Local artifacts and temp directories</p>
              </div>
            </div>
            
            <div class="grid gap-10 sm:grid-cols-2">
              ${diskBar('Source Uploads', disk['uploads'] || 0, 'Temporary incoming chunks')}
              ${diskBar('Converted Files', disk['converted'] || 0, 'Ready for user download')}
            </div>
          </section>

          <!-- Quick Actions -->
          <section class="panel rounded-3xl p-8 border-slate-200/60 bg-slate-50/30">
            <div class="flex items-center gap-3 mb-6">
              <h2 class="text-sm font-bold uppercase tracking-widest text-slate-400">Maintenance</h2>
            </div>
            <div class="flex flex-wrap gap-3">
              <button class="btn btn-secondary bg-white shadow-sm" type="button" data-overview-tab="users" data-disabled="true">
                <i data-lucide="user-minus" class="h-4 w-4 text-slate-400"></i>
                Review Disabled Users
              </button>
              <button class="btn btn-secondary bg-white shadow-sm" type="button" data-overview-tab="events" data-kind="job.removed">
                <i data-lucide="history" class="h-4 w-4 text-slate-400"></i>
                Audit Destructive Actions
              </button>
              <button class="btn btn-secondary bg-white shadow-sm" type="button" data-overview-tab="settings">
                <i data-lucide="settings-2" class="h-4 w-4 text-slate-400"></i>
                Update Global Limits
              </button>
            </div>
          </section>
        </div>

        <!-- Activity Feed -->
        <aside class="space-y-6">
          <div class="flex items-center justify-between px-2">
            <h2 class="text-sm font-bold uppercase tracking-widest text-slate-400">Recent Activity</h2>
            <button class="text-[10px] font-black uppercase text-brand-600 hover:underline" data-overview-tab="events">View Logs</button>
          </div>
          
          <div class="panel rounded-3xl overflow-hidden border-slate-200/60 flex flex-col divide-y divide-slate-100">
            ${recentEvents.events.length === 0 ? '<div class="p-10 text-center text-xs font-bold text-slate-400 uppercase tracking-widest">No activity recorded</div>' : ''}
            ${recentEvents.events.map(event => `
              <div class="p-4 hover:bg-slate-50/50 transition-colors group">
                <div class="flex items-start gap-3">
                  <div class="mt-1 h-2 w-2 rounded-full shrink-0 ${event.level === 'error' ? 'bg-red-500' : event.level === 'warn' ? 'bg-amber-400' : 'bg-slate-200'}"></div>
                  <div class="min-w-0 flex-1">
                    <p class="text-xs font-bold text-slate-900 leading-snug line-clamp-2">${escapeHTML(event.message)}</p>
                    <div class="mt-1 flex items-center gap-2 text-[9px] font-black uppercase tracking-tighter text-slate-400">
                      <span>${escapeHTML(event.kind)}</span>
                      <span class="h-1 w-1 rounded-full bg-slate-200"></span>
                      <span class="tabular-nums">${new Date(event.createdAt).toLocaleTimeString()}</span>
                    </div>
                  </div>
                </div>
              </div>
            `).join('')}
          </div>

          <div class="flex items-center justify-between px-2 pt-2">
            <h2 class="text-sm font-bold uppercase tracking-widest text-slate-400">Latest Jobs</h2>
            <button class="text-[10px] font-black uppercase text-brand-600 hover:underline" data-overview-tab="jobs">View All</button>
          </div>

          <div class="panel rounded-3xl overflow-hidden border-slate-200/60 flex flex-col divide-y divide-slate-100">
            ${recentJobs.jobs.length === 0 ? '<div class="p-10 text-center text-xs font-bold text-slate-400 uppercase tracking-widest">No jobs found</div>' : ''}
            ${recentJobs.jobs.map(job => `
              <div class="p-4 hover:bg-slate-50/50 transition-colors flex items-center justify-between gap-4">
                <div class="min-w-0 flex-1">
                  <div class="flex items-center gap-2">
                    <span class="font-mono text-[10px] font-bold text-slate-900">${shortID(job.id)}</span>
                    <span class="inline-flex items-center px-1.5 py-0.5 rounded bg-slate-100 text-[8px] font-black uppercase text-slate-500">${escapeHTML(job.targetFormat)}</span>
                  </div>
                  <div class="mt-1 flex items-center gap-2">
                    <div class="h-1 flex-1 bg-slate-100 rounded-full overflow-hidden">
                      <div class="h-full bg-brand-500" style="width: ${job.progressPercentage || 0}%"></div>
                    </div>
                    <span class="text-[9px] font-bold text-slate-400 tabular-nums">${job.progressPercentage || 0}%</span>
                  </div>
                </div>
                <div>${statusBadge(job.status)}</div>
              </div>
            `).join('')}
          </div>
        </aside>
      </div>
    </div>
  `;
}

function statusRow(label: string, count: number, colorClass: string) {
  return `
    <div class="flex items-center justify-between gap-4">
      <div class="flex items-center gap-2 min-w-0">
        <div class="h-2 w-2 rounded-full ${colorClass}"></div>
        <span class="text-xs font-bold text-slate-600 uppercase tracking-tight truncate">${label}</span>
      </div>
      <span class="text-xs font-black text-slate-900 tabular-nums">${count}</span>
    </div>
  `;
}

function diskBar(label: string, bytes: number, sub: string) {
  return `
    <div class="space-y-3">
      <div class="flex items-baseline justify-between gap-2">
        <h3 class="text-sm font-bold text-slate-900">${label}</h3>
        <span class="text-xs font-black text-slate-700 tabular-nums">${humanBytes(bytes)}</span>
      </div>
      <div class="h-2 w-full rounded-full bg-slate-100 overflow-hidden shadow-inner">
        <div class="h-full bg-slate-900 rounded-full transition-all duration-1000" style="width: ${Math.min(100, (bytes / (1024 ** 3 * 10)) * 100)}%"></div>
      </div>
      <p class="text-[10px] font-bold text-slate-400 uppercase tracking-widest">${sub}</p>
    </div>
  `;
}
