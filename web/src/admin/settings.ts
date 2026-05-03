import { patchSettings, settings } from './api';
import { escapeHTML, readForm, toast } from './components';

type SettingType = 'boolean' | 'number' | 'bytes' | 'duration_min' | 'duration_hour' | 'duration_day';

interface SettingMeta {
  label: string;
  description: string;
  type: SettingType;
  group: SettingGroup;
}

type SettingGroup = 'access' | 'storage' | 'performance' | 'security' | 'retention';

const GROUPS: Record<SettingGroup, { label: string; description: string; icon: string }> = {
  access: {
    label: 'General Access',
    description: 'Control who can use the platform and how.',
    icon: 'user-check'
  },
  storage: {
    label: 'Upload & Storage',
    description: 'Manage file size limits, chunking, and disk space safety.',
    icon: 'hard-drive'
  },
  performance: {
    label: 'Processing & Performance',
    description: 'Optimize conversion speeds and server resource usage.',
    icon: 'cpu'
  },
  security: {
    label: 'Anti-Abuse & Limits',
    description: 'Prevent system overload and rate limit users by IP.',
    icon: 'shield-alert'
  },
  retention: {
    label: 'Cleanup & Retention',
    description: 'Define how long files and logs stay on the server.',
    icon: 'trash-2'
  }
};

const SETTING_META: Record<string, SettingMeta> = {
  public_uploads_enabled: {
    label: 'Public Uploads',
    description: 'Allow anonymous users to upload and convert files without an account.',
    type: 'boolean',
    group: 'access'
  },
  max_upload_bytes: {
    label: 'Maximum Upload Size',
    description: 'The largest file size accepted by the system per upload.',
    type: 'bytes',
    group: 'storage'
  },
  chunk_size_bytes: {
    label: 'Upload Chunk Size',
    description: 'Size of individual chunks for resilient, resumable uploads.',
    type: 'bytes',
    group: 'storage'
  },
  min_free_disk_bytes: {
    label: 'Minimum Free Disk Space',
    description: 'Stop accepting new uploads if free disk space falls below this threshold.',
    type: 'bytes',
    group: 'storage'
  },
  max_concurrent_jobs: {
    label: 'Global Parallel Jobs',
    description: 'Maximum number of conversion jobs running simultaneously on the server.',
    type: 'number',
    group: 'performance'
  },
  max_queue_depth: {
    label: 'Job Queue Limit',
    description: 'Maximum number of jobs allowed in the waiting queue across the system.',
    type: 'number',
    group: 'performance'
  },
  conversion_timeout_minutes: {
    label: 'Conversion Timeout',
    description: 'Maximum time a conversion job is allowed to run before termination.',
    type: 'duration_min',
    group: 'performance'
  },
  upload_inactivity_timeout_minutes: {
    label: 'Upload Inactivity Timeout',
    description: 'Abandon uploads that have seen no chunk activity for this duration.',
    type: 'duration_min',
    group: 'performance'
  },
  max_active_uploads_per_ip: {
    label: 'Active Uploads per IP',
    description: 'Maximum concurrent uploads allowed from a single IP address.',
    type: 'number',
    group: 'security'
  },
  max_upload_starts_per_ip_per_hour: {
    label: 'Upload Starts per Hour',
    description: 'Rate limit for starting new uploads from a single IP address.',
    type: 'number',
    group: 'security'
  },
  max_jobs_per_ip_per_day: {
    label: 'Daily Jobs per IP',
    description: 'Total number of conversion jobs allowed per IP per day.',
    type: 'number',
    group: 'security'
  },
  finished_file_retention_hours: {
    label: 'Finished File Retention',
    description: 'How long to keep converted files available for user download.',
    type: 'duration_hour',
    group: 'retention'
  },
  failed_upload_retention_hours: {
    label: 'Failed Upload Retention',
    description: 'How long to keep partial or failed uploads before cleanup.',
    type: 'duration_hour',
    group: 'retention'
  },
  event_retention_days: {
    label: 'System Log Retention',
    description: 'Number of days to keep system activity and job event logs.',
    type: 'duration_day',
    group: 'retention'
  }
};

const DEFAULT_SETTINGS: Record<string, string> = {
  public_uploads_enabled: 'true',
  max_upload_bytes: '10737418240',
  chunk_size_bytes: '16777216',
  max_queue_depth: '100',
  max_active_uploads_per_ip: '2',
  max_upload_starts_per_ip_per_hour: '10',
  max_jobs_per_ip_per_day: '25',
  max_concurrent_jobs: '1',
  conversion_timeout_minutes: '240',
  upload_inactivity_timeout_minutes: '30',
  finished_file_retention_hours: '24',
  failed_upload_retention_hours: '24',
  event_retention_days: '30',
  min_free_disk_bytes: '21474836480'
};

let currentSettings: Record<string, string> = {};
let hasChanges = false;

export async function renderSettings() {
  const result = await settings();
  currentSettings = { ...result.settings };
  hasChanges = false;
  
  const groups = Object.keys(GROUPS) as SettingGroup[];

  return `
    <div class="space-y-10 animate-in fade-in duration-500">
      <div class="flex flex-wrap items-center justify-between gap-6 border-b border-slate-200 pb-8">
        <div>
          <h1 class="text-3xl font-black tracking-tight text-slate-900">System Configuration</h1>
          <p class="mt-1 text-sm font-medium text-slate-500 text-balance">Manage global limits, security policies, and performance settings for the media engine.</p>
        </div>
        <div class="flex items-center gap-3">
          <button class="btn btn-ghost h-10 px-4 text-slate-500" type="button" data-settings-reset>
            <i data-lucide="rotate-ccw" class="h-4 w-4"></i>
            Reset Defaults
          </button>
          <button class="btn btn-primary h-10 shadow-lg shadow-brand-100" type="submit" form="settings-form">
            <i data-lucide="save" class="h-4 w-4"></i>
            Save Configuration
          </button>
        </div>
      </div>

      <div id="save-hint" class="hidden sticky top-6 z-20 animate-in slide-in-from-top-4 duration-300">
        <div class="flex items-center justify-between gap-4 rounded-2xl bg-white border-2 border-brand-500 px-6 py-4 shadow-2xl">
          <div class="flex items-center gap-3">
            <div class="flex h-8 w-8 items-center justify-center rounded-lg bg-brand-500 text-white">
              <i data-lucide="alert-circle" class="h-4 w-4"></i>
            </div>
            <div>
              <p class="text-sm font-bold text-slate-900">Unsaved configuration changes</p>
              <p class="text-[10px] font-bold uppercase tracking-widest text-slate-400">Remember to save to apply updates</p>
            </div>
          </div>
          <button class="btn btn-primary h-9 px-6 py-0 shadow-md" type="submit" form="settings-form">Save Changes</button>
        </div>
      </div>

      <form id="settings-form" class="space-y-12" data-settings-form>
        ${groups.map(group => renderGroup(group, currentSettings)).join('')}
      </form>
    </div>
  `;
}

function renderGroup(groupId: SettingGroup, allSettings: Record<string, string>) {
  const group = GROUPS[groupId];
  const relevantSettings = Object.entries(SETTING_META).filter(([_, meta]) => meta.group === groupId);
  
  return `
    <section class="grid gap-8 lg:grid-cols-[18rem_1fr]">
      <div class="space-y-3">
        <div class="flex h-12 w-12 items-center justify-center rounded-2xl bg-slate-900 text-white shadow-lg shadow-slate-200">
          <i data-lucide="${group.icon}" class="h-6 w-6"></i>
        </div>
        <div>
          <h2 class="text-lg font-bold text-slate-900">${escapeHTML(group.label)}</h2>
          <p class="mt-1 text-sm font-medium text-slate-500 leading-relaxed">${escapeHTML(group.description)}</p>
        </div>
      </div>
      
      <div class="panel divide-y divide-slate-100 rounded-3xl overflow-hidden shadow-sm">
        ${relevantSettings.map(([key, meta]) => `
          <div class="grid gap-6 p-8 sm:grid-cols-[1fr_16rem] transition-colors hover:bg-slate-50/30">
            <div class="space-y-1.5">
              <h3 class="text-sm font-bold text-slate-900">${escapeHTML(meta.label)}</h3>
              <p class="text-xs font-medium text-slate-500 leading-relaxed max-w-md">${escapeHTML(meta.description)}</p>
            </div>
            <div class="flex items-center">
              ${renderFieldInput(key, allSettings[key], meta)}
            </div>
          </div>
        `).join('')}
      </div>
    </section>
  `;
}

function renderFieldInput(key: string, value: string, meta: SettingMeta) {
  if (meta.type === 'boolean') {
    return `
      <div class="flex h-10 w-full items-center gap-1 rounded-xl bg-slate-100 p-1 border border-slate-200">
        <label class="flex flex-1 items-center justify-center gap-2 cursor-pointer rounded-lg px-3 py-1.5 transition-all has-[:checked]:bg-white has-[:checked]:text-brand-600 has-[:checked]:shadow-sm">
          <input type="radio" name="${key}" value="true" ${value === 'true' ? 'checked' : ''} class="sr-only" />
          <span class="text-[10px] font-black uppercase tracking-widest">On</span>
        </label>
        <label class="flex flex-1 items-center justify-center gap-2 cursor-pointer rounded-lg px-3 py-1.5 transition-all has-[:checked]:bg-white has-[:checked]:text-slate-900 has-[:checked]:shadow-sm">
          <input type="radio" name="${key}" value="false" ${value === 'false' ? 'checked' : ''} class="sr-only" />
          <span class="text-[10px] font-black uppercase tracking-widest">Off</span>
        </label>
      </div>
    `;
  }

  if (meta.type === 'bytes') {
    const { value: v, unit } = formatBytesForInput(value);
    return `
      <div class="flex w-full overflow-hidden rounded-xl border border-slate-200 bg-white focus-within:ring-2 focus-within:ring-brand-500/20 focus-within:border-brand-500 transition-all">
        <input class="w-full min-w-0 border-0 bg-transparent px-4 py-2.5 text-right tabular-nums font-bold text-sm focus:ring-0" type="number" step="any" data-byte-value="${key}" value="${v}" />
        <select class="w-24 border-0 border-l border-slate-100 bg-slate-50/50 px-3 py-2 text-center text-[10px] font-black uppercase tracking-wider text-slate-500 focus:ring-0" data-byte-unit="${key}">
          ${['B', 'KB', 'MB', 'GB', 'TB'].map(u => `<option value="${u}" ${u === unit ? 'selected' : ''}>${u}</option>`).join('')}
        </select>
        <input type="hidden" name="${key}" value="${value}" />
      </div>
    `;
  }

  const suffix = meta.type === 'duration_min' ? 'min' : meta.type === 'duration_hour' ? 'hrs' : meta.type === 'duration_day' ? 'days' : '';
  return `
    <div class="flex w-full overflow-hidden rounded-xl border border-slate-200 bg-white focus-within:ring-2 focus-within:ring-brand-500/20 focus-within:border-brand-500 transition-all">
      <input class="w-full min-w-0 border-0 bg-transparent px-4 py-2.5 text-right tabular-nums font-bold text-sm focus:ring-0" type="number" name="${key}" value="${value}" />
      ${suffix ? `<div class="flex items-center justify-center w-16 border-0 border-l border-slate-100 bg-slate-50/50 text-[10px] font-black uppercase tracking-widest text-slate-400 pointer-events-none">${suffix}</div>` : ''}
    </div>
  `;
}

function formatBytesForInput(bytes: string): { value: string; unit: string } {
  const b = parseInt(bytes, 10);
  if (b === 0) return { value: '0', unit: 'B' };
  
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = b;
  let u = 0;
  
  // Try to find the largest unit that results in an integer
  while (v >= 1024 && v % 1024 === 0 && u < units.length - 1) {
    v /= 1024;
    u++;
  }
  
  // If we couldn't find an integer representation, find the best fit
  if (v >= 1024 && u < units.length - 1) {
    let bestV = b;
    let bestU = 0;
    while (bestV >= 1024 && bestU < units.length - 1) {
      bestV /= 1024;
      bestU++;
    }
    return { value: bestV.toFixed(1), unit: units[bestU] };
  }

  return { value: v.toString(), unit: units[u] };
}

function parseBytes(value: string, unit: string): string {
  const num = parseFloat(value);
  const factor = { B: 1, KB: 1024, MB: 1024 ** 2, GB: 1024 ** 3, TB: 1024 ** 4 }[unit] || 1;
  return Math.round(num * factor).toString();
}

export function bindSettings(root: HTMLElement, rerender: () => Promise<void>) {
  const form = root.querySelector<HTMLFormElement>('#settings-form');
  const saveHint = root.querySelector<HTMLElement>('#save-hint');

  const checkChanges = () => {
    const data = readForm(form!);
    hasChanges = Object.entries(data).some(([key, value]) => currentSettings[key] !== value);
    if (hasChanges) {
      saveHint?.classList.remove('hidden');
    } else {
      saveHint?.classList.add('hidden');
    }
  };

  form?.addEventListener('input', (e) => {
    const target = e.target as HTMLElement;
    
    if (target.hasAttribute('data-byte-value') || target.hasAttribute('data-byte-unit')) {
      const key = target.getAttribute('data-byte-value') || target.getAttribute('data-byte-unit')!;
      const valInput = form.querySelector<HTMLInputElement>(`[data-byte-value="${key}"]`)!;
      const unitSelect = form.querySelector<HTMLSelectElement>(`[data-byte-unit="${key}"]`)!;
      const hiddenInput = form.querySelector<HTMLInputElement>(`input[name="${key}"]`)!;
      hiddenInput.value = parseBytes(valInput.value, unitSelect.value);
    }
    
    checkChanges();
  });

  form?.addEventListener('submit', async (event) => {
    event.preventDefault();
    const data = readForm(form);
    await patchSettings(data);
    toast('System configuration updated.');
    await rerender();
  });

  root.querySelector('[data-settings-reset]')?.addEventListener('click', () => {
    Object.entries(DEFAULT_SETTINGS).forEach(([key, value]) => {
      const meta = SETTING_META[key];
      if (meta?.type === 'boolean') {
        const radio = form?.querySelector<HTMLInputElement>(`input[name="${key}"][value="${value}"]`);
        if (radio) radio.checked = true;
      } else if (meta?.type === 'bytes') {
        const { value: v, unit } = formatBytesForInput(value);
        const valInput = form?.querySelector<HTMLInputElement>(`[data-byte-value="${key}"]`);
        const unitSelect = form?.querySelector<HTMLSelectElement>(`[data-byte-unit="${key}"]`);
        const hiddenInput = form?.querySelector<HTMLInputElement>(`input[name="${key}"]`);
        if (valInput) valInput.value = v;
        if (unitSelect) unitSelect.value = unit;
        if (hiddenInput) hiddenInput.value = value;
      } else {
        const input = form?.querySelector<HTMLInputElement>(`input[name="${key}"]`);
        if (input) input.value = value;
      }
    });
    checkChanges();
    toast('Settings reset to defaults. Save to apply.');
  });
}
