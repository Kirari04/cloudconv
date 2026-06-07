import {
  Activity,
  AlertCircle,
  ArrowLeft,
  Check,
  ChevronLeft,
  ChevronRight,
  Clock,
  Copy,
  Cpu,
  Download,
  Edit3,
  ExternalLink,
  FileAudio,
  FileImage,
  FileVideo,
  FileWarning,
  Files,
  Filter,
  HardDrive,
  History,
  Image,
  Info,
  Layers,
  LayoutDashboard,
  LogIn,
  LogOut,
  Music,
  Pause,
  PauseCircle,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Save,
  SearchX,
  Settings,
  Settings2,
  Shield,
  ShieldAlert,
  ShieldCheck,
  Trash2,
  Upload,
  UploadCloud,
  User,
  UserCheck,
  UserMinus,
  UserPlus,
  UserX,
  Video,
  X,
  XCircle,
  Zap,
  createIcons
} from 'lucide';
import './styles.css';
import { loadConfig, type AppConfig } from './api';
import { login, logout, setup } from './auth';
import {
  audioCodecSupportsBitrate,
  detectMediaType,
  formatById,
  formatsFor,
  humanBytes,
  presetById,
  presetEffectFor,
  presetEffectKeyFor,
  presetOptionLabel,
  presetPlaceholder,
  presetSummaryRows,
  resetInvalidCodecOptions,
  type FormatOption,
  type MediaType
} from './catalog';
import { renderAdmin } from './admin/index';
import { uploadAndConvert, type UploadController, type UploadOptions, type UploadProgress } from './upload';

type FileItem = {
  id: string;
  file: File;
  type: MediaType | 'unknown';
  progress?: UploadProgress;
  controller?: UploadController;
  queued?: boolean;
  queuedOptions?: UploadOptions;
  uploadSlotReleased?: boolean;
};

type GroupState = {
  targetFormat: string;
  preset: string;
  advanced: boolean;
  options: Record<string, number | boolean | string>;
};

const foundAppRoot = document.querySelector<HTMLDivElement>('#app');
if (!foundAppRoot) throw new Error('App root missing');
const appRoot: HTMLDivElement = foundAppRoot;

void boot();

async function boot() {
  const config = await loadConfig();
  const path = window.location.pathname;
  if (path === '/admin') {
    if (config.auth.user?.role !== 'admin') {
      renderLogin(config, 'Admin access requires login.');
    } else {
      await renderAdmin(appRoot, config.auth);
      activateIcons();
    }
    return;
  }
  if (path === '/login') {
    renderLogin(config);
    return;
  }
  if (path === '/setup') {
    renderSetup(config);
    return;
  }
  if (config.setupNeeded) {
    renderSetup(config);
    return;
  }
  renderConverter(config);
}

function renderConverter(config: AppConfig) {
  const files: FileItem[] = [];
  const states = defaultGroupStates(config);
  const configuredUploadConcurrency = Number.parseInt(String(config.settings.max_active_uploads_per_ip || '1'), 10);
  const maxUploadConcurrency = configuredUploadConcurrency > 0 ? configuredUploadConcurrency : 1;
  let activeUploads = 0;
  appRoot.innerHTML = `
    <main class="shell mx-auto flex w-full max-w-6xl flex-col gap-8 px-6 py-10">
      ${topbar(config)}
      
      <section class="grid gap-8 lg:grid-cols-[1fr_20rem]">
        <div class="flex flex-col gap-6">
          <div id="dropzone" class="dropzone group relative flex flex-col items-center justify-center rounded-2xl p-12 transition-all">
            <div class="flex flex-col items-center gap-4 text-center">
              <div class="flex h-16 w-16 items-center justify-center rounded-2xl bg-brand-50 text-brand-600 transition-transform group-hover:scale-110">
                <i data-lucide="upload" class="h-8 w-8"></i>
              </div>
              <div class="space-y-1">
                <h1 class="text-2xl font-bold tracking-tight text-slate-900">Convert your media</h1>
                <p class="mx-auto max-w-sm text-sm text-slate-500">Drag and drop video, audio, or images here to start your conversion.</p>
              </div>
              <label class="btn btn-primary mt-2 cursor-pointer shadow-md">
                <i data-lucide="plus" class="h-4 w-4"></i>
                Choose Files
                <input id="file-input" class="sr-only" type="file" multiple />
              </label>
              <p class="text-[11px] font-medium uppercase tracking-wider text-slate-400">Max size ${humanBytes(Number(config.settings.max_upload_bytes || 0))}</p>
            </div>
          </div>

          <div id="file-list-container" class="space-y-4">
            <div class="flex items-center justify-between px-2">
              <h2 class="text-sm font-bold uppercase tracking-widest text-slate-400">Files to convert</h2>
              <div id="file-count" class="text-xs font-semibold text-slate-500">0 files</div>
            </div>
            <section id="file-list" class="flex flex-col gap-4"></section>
          </div>
        </div>

        <aside id="options" class="flex flex-col gap-6"></aside>
      </section>
    </main>
  `;
  const dropzone = document.querySelector<HTMLElement>('#dropzone')!;
  const fileInput = document.querySelector<HTMLInputElement>('#file-input')!;
  const fileList = document.querySelector<HTMLElement>('#file-list')!;
  const options = document.querySelector<HTMLElement>('#options')!;
  const fileCount = document.querySelector<HTMLElement>('#file-count')!;

  const addFiles = (selected: FileList | File[]) => {
    Array.from(selected).forEach((file) => {
      files.push({ id: crypto.randomUUID(), file, type: detectMediaType(file) });
    });
    renderAll();
  };

  dropzone.addEventListener('dragover', (event) => {
    event.preventDefault();
    dropzone.classList.add('dragging');
  });
  dropzone.addEventListener('dragleave', () => dropzone.classList.remove('dragging'));
  dropzone.addEventListener('drop', (event) => {
    event.preventDefault();
    dropzone.classList.remove('dragging');
    if (event.dataTransfer?.files) addFiles(event.dataTransfer.files);
  });
  fileInput.addEventListener('change', () => {
    if (fileInput.files) addFiles(fileInput.files);
    fileInput.value = '';
  });

  function renderAll() {
    fileCount.textContent = `${files.length} file${files.length === 1 ? '' : 's'}`;
    renderOptions();
    renderFiles();
    activateIcons();
  }

  function renderOptions() {
    const presentTypes = new Set(files.map((item) => item.type).filter((type): type is MediaType => type !== 'unknown'));
    options.innerHTML = `
      <section class="panel sticky top-10 flex flex-col gap-6 rounded-2xl p-6">
        <div class="flex items-center justify-between border-b border-slate-100 pb-4">
          <h2 class="font-bold text-slate-900">Settings</h2>
          <button id="start-all" class="btn btn-primary h-9 px-4 py-0" type="button" ${startableFiles().length === 0 ? 'disabled' : ''}>
            <i data-lucide="play" class="h-3.5 w-3.5"></i> Start
          </button>
        </div>
        
        <div class="space-y-10">
          ${files.length === 0 ? '<p class="text-center text-sm text-slate-400 py-4 font-medium italic">Add files to configure</p>' : ''}
          ${Array.from(presentTypes).map((type) => groupControls(type, states[type], config)).join('')}
        </div>

        ${files.some((item) => item.type === 'unknown') ? `
          <div class="flex gap-3 rounded-xl bg-amber-50 p-4 text-xs font-medium text-amber-800 border border-amber-100/50">
            <i data-lucide="alert-circle" class="h-4 w-4 shrink-0"></i>
            <p>Some files have unsupported formats and will be skipped.</p>
          </div>
        ` : ''}
      </section>
    `;
    options.querySelectorAll<HTMLButtonElement>('[data-set-format]').forEach((button) => {
      button.addEventListener('click', () => {
        const [type, formatId] = (button.dataset.setFormat || '').split(':') as [MediaType, string];
        states[type].targetFormat = formatId;
        resetInvalidCodecOptions(states[type].options, formatById(config.catalog, formatId));
        if (formatId === 'gif' && states[type].options.loop === undefined) {
          states[type].options.loop = true;
        }
        renderAll();
      });
    });
    options.querySelectorAll<HTMLSelectElement>('[data-preset]').forEach((select) => {
      select.addEventListener('change', () => {
        states[select.dataset.preset as MediaType].preset = select.value;
        renderAll();
      });
    });
    options.querySelectorAll<HTMLInputElement | HTMLSelectElement>('[data-option]').forEach((input) => {
      input.addEventListener('change', () => {
        const [type, key] = (input.dataset.option || '').split(':') as [MediaType, string];
        if (input instanceof HTMLInputElement && input.type === 'checkbox') {
          states[type].options[key] = input.checked;
        } else if (input instanceof HTMLInputElement && input.type === 'number') {
          states[type].options[key] = Number(input.value || 0);
        } else {
          states[type].options[key] = input.value;
        }
        if (type === 'video' && key === 'audioCodec') {
          const format = formatById(config.catalog, states.video.targetFormat);
          if (!audioCodecSupportsBitrate(format, String(states.video.options.audioCodec || ''))) {
            delete states.video.options.audioBitrate;
          }
        }
        renderAll();
      });
    });
    options.querySelectorAll<HTMLButtonElement>('[data-advanced]').forEach((button) => {
      button.addEventListener('click', () => {
        const type = button.dataset.advanced as MediaType;
        states[type].advanced = !states[type].advanced;
        renderAll();
      });
    });
    options.querySelector('#start-all')?.addEventListener('click', () => startAll());
  }

  function renderFiles() {
    fileList.innerHTML = files.length === 0 
      ? `
        <div class="flex flex-col items-center justify-center py-20 opacity-40">
          <i data-lucide="files" class="h-12 w-12 text-slate-300"></i>
          <p class="mt-4 text-sm font-medium text-slate-500">No files selected</p>
        </div>
      ` 
      : files.map((item) => fileRow(item)).join('');
      
    fileList.querySelectorAll<HTMLButtonElement>('[data-remove]').forEach((button) => {
      button.addEventListener('click', () => {
        const index = files.findIndex((item) => item.id === button.dataset.remove);
        if (index > -1) files.splice(index, 1);
        renderAll();
      });
    });
    fileList.querySelectorAll<HTMLButtonElement>('[data-pause]').forEach((button) => {
      button.addEventListener('click', () => files.find((item) => item.id === button.dataset.pause)?.controller?.pause());
    });
    fileList.querySelectorAll<HTMLButtonElement>('[data-resume]').forEach((button) => {
      button.addEventListener('click', () => files.find((item) => item.id === button.dataset.resume)?.controller?.resume());
    });
    fileList.querySelectorAll<HTMLButtonElement>('[data-cancel]').forEach((button) => {
      button.addEventListener('click', () => files.find((item) => item.id === button.dataset.cancel)?.controller?.cancel());
    });
  }

  function startAll() {
    startableFiles().forEach((item) => {
      const state = states[item.type as MediaType];
      item.queued = true;
      item.queuedOptions = {
        targetFormat: state.targetFormat,
        preset: state.preset,
        options: { ...state.options }
      };
      item.progress = {
        phase: 'queued',
        uploadPercent: 0,
        convertPercent: 0,
        message: 'Waiting to upload'
      };
    });
    renderAll();
    pumpUploadQueue();
  }

  function startableFiles() {
    return files.filter((item) => item.type !== 'unknown' && !item.controller && !item.queued);
  }

  function pumpUploadQueue() {
    while (activeUploads < maxUploadConcurrency) {
      const item = files.find((entry) => entry.queued && !entry.controller && entry.type !== 'unknown');
      if (!item) return;
      const queuedOptions = item.queuedOptions;
      if (!queuedOptions) {
        item.queued = false;
        continue;
      }
      item.queued = false;
      item.uploadSlotReleased = false;
      activeUploads += 1;
      item.controller = uploadAndConvert(item.file, queuedOptions, (progress) => {
        item.progress = progress;
        if (!item.uploadSlotReleased && progress.phase !== 'uploading') {
          item.uploadSlotReleased = true;
          activeUploads = Math.max(0, activeUploads - 1);
          queueMicrotask(pumpUploadQueue);
        }
        renderFiles();
        activateIcons();
      });
    }
    renderFiles();
    activateIcons();
  }
  renderAll();
}

function defaultGroupStates(config: AppConfig): Record<MediaType, GroupState> {
  const first = (type: MediaType) => {
    const formats = formatsFor(config.catalog, type);
    return formats.find((format) => format.mediaType === type)?.id || formats[0]?.id || 'mp4';
  };
  const state = (type: MediaType): GroupState => {
    const targetFormat = first(type);
    return {
      targetFormat,
      preset: 'balanced',
      advanced: false,
      options: targetFormat === 'gif' ? { loop: true } : {}
    };
  };
  return {
    video: state('video'),
    audio: state('audio'),
    image: state('image')
  };
}

function groupControls(type: MediaType, state: GroupState, config: AppConfig) {
  const formats = formatsFor(config.catalog, type);
  const icon = type === 'video' ? 'video' : type === 'audio' ? 'music' : 'image';
  
  const groupedFormats = formats.reduce((acc, f) => {
    const key = f.mediaType;
    if (!acc[key]) acc[key] = [];
    acc[key].push(f);
    return acc;
  }, {} as Record<string, typeof formats>);

  return `
    <div class="space-y-6">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2">
          <div class="flex h-8 w-8 items-center justify-center rounded-lg bg-slate-100 text-slate-600">
            <i data-lucide="${icon}" class="h-4 w-4"></i>
          </div>
          <h3 class="text-sm font-bold capitalize text-slate-900">${type} Settings</h3>
        </div>
        <button class="btn btn-ghost h-8 px-2 py-0 text-[11px] font-bold uppercase tracking-wider" data-advanced="${type}" type="button">
          <i data-lucide="settings-2" class="h-3.5 w-3.5"></i> ${state.advanced ? 'Simple' : 'Advanced'}
        </button>
      </div>

      <div class="space-y-4">
        <div class="text-[10px] font-black uppercase tracking-widest text-slate-400">Target Output Format</div>
        <div class="flex flex-col gap-5">
          ${Object.entries(groupedFormats).map(([group, list]) => `
            <div class="space-y-2">
              <div class="text-[9px] font-black uppercase tracking-widest text-slate-400/80 px-1 border-l-2 border-slate-100 ml-1 pl-2">${group}</div>
              <div class="grid grid-cols-3 gap-2">
                ${list.map((format) => `
                  <button 
                    class="group relative flex flex-col items-center justify-center rounded-xl border-2 p-3 transition-all ${state.targetFormat === format.id ? 'border-brand-500 bg-brand-50/50 ring-4 ring-brand-500/10' : 'border-slate-100 bg-white hover:border-slate-300 hover:bg-slate-50'}"
                    type="button"
                    data-set-format="${type}:${format.id}"
                  >
                    <span class="text-xs font-black uppercase tracking-tight ${state.targetFormat === format.id ? 'text-brand-700' : 'text-slate-600'}">${format.id}</span>
                    <span class="mt-0.5 text-[8px] font-bold text-slate-400 opacity-0 transition-opacity group-hover:opacity-100 absolute bottom-1 truncate w-full text-center px-1">${format.label}</span>
                    ${state.targetFormat === format.id ? `
                      <div class="absolute -right-1 -top-1 flex h-4 w-4 items-center justify-center rounded-full bg-brand-600 text-white shadow-sm ring-2 ring-white">
                        <i data-lucide="check" class="h-2.5 w-2.5"></i>
                      </div>
                    ` : ''}
                  </button>
                `).join('')}
              </div>
            </div>
          `).join('')}
        </div>
      </div>
      
      <div class="grid gap-4 pt-2">
        <label class="space-y-1.5">
          <span class="text-[10px] font-black uppercase tracking-widest text-slate-400">Processing Preset</span>
          <select class="field bg-slate-50/50" data-preset="${type}">
            ${config.catalog.presets.map((preset) => `<option value="${preset}" ${state.preset === preset ? 'selected' : ''}>${escapeHTML(presetOptionLabel(config.catalog, preset, type, state.targetFormat))}</option>`).join('')}
          </select>
        </label>

        ${presetSummary(type, state, config)}

        ${state.advanced ? `
          <div class="mt-2 space-y-4 rounded-2xl bg-slate-50/50 p-5 border border-slate-100 shadow-inner">
            <div class="text-[10px] font-black uppercase tracking-widest text-slate-400 mb-2">Technical Overrides</div>
            ${advancedControls(type, state, config)}
          </div>
        ` : ''}
      </div>
    </div>
  `;
}

function presetSummary(type: MediaType, state: GroupState, config: AppConfig) {
  const format = formatById(config.catalog, state.targetFormat);
  const preset = presetById(config.catalog, state.preset);
  const effect = presetEffectFor(config.catalog, state.preset, type, state.targetFormat);
  const rows = presetSummaryRows(type, state.options, format, effect);
  const title = `${preset?.label || label(state.preset)} preset`;
  const summary = effect?.summary || preset?.summary || 'Preset defaults';
  return `
    <div class="rounded-xl border border-brand-100 bg-brand-50/60 p-3 text-xs text-slate-600">
      <div class="flex items-start justify-between gap-3">
        <div>
          <div class="font-bold leading-snug text-slate-900">${escapeHTML(title)}</div>
          <p class="mt-0.5 leading-snug text-slate-600">${escapeHTML(summary)}</p>
        </div>
        <span class="shrink-0 rounded-full bg-white px-2 py-1 text-[10px] font-black uppercase tracking-wider text-brand-600 shadow-sm">${escapeHTML(state.preset)}</span>
      </div>
      <dl class="mt-2 grid gap-1.5">
        ${rows.map((row) => `
          <div class="flex justify-between gap-3">
            <dt class="text-slate-500">${escapeHTML(row.label)}</dt>
            <dd class="text-right font-semibold text-slate-700">${escapeHTML(row.value)}</dd>
          </div>
        `).join('')}
      </dl>
    </div>
  `;
}

function advancedControls(type: MediaType, state: GroupState, config: AppConfig) {
  const format = formatById(config.catalog, state.targetFormat);
  const effectKey = presetEffectKeyFor(type, format);
  if (effectKey === 'gif') {
    return `
      <div class="grid gap-4">
        ${numberField(type, 'maxWidth', 'Width', state.options.maxWidth, presetPlaceholder(config.catalog, state.preset, type, state.targetFormat, 'maxWidth', '480'))}
        ${numberField(type, 'framerate', 'FPS', state.options.framerate, presetPlaceholder(config.catalog, state.preset, type, state.targetFormat, 'framerate', '15'))}
        <label class="flex items-center gap-2.5 cursor-pointer">
          <input type="checkbox" class="h-4 w-4 rounded border-slate-300 text-brand-600 focus:ring-brand-500/20" data-option="${type}:loop" ${(state.options.loop ?? true) ? 'checked' : ''}/>
          <span class="text-xs font-semibold text-slate-700">Loop GIFs</span>
        </label>
      </div>
    `;
  }
  if (effectKey === 'video') {
    const audioSupportsBitrate = audioCodecSupportsBitrate(format, String(state.options.audioCodec || ''));
    return `
      <div class="grid gap-4">
        ${codecSelect(type, 'videoCodec', 'Video codec', state.options.videoCodec, format?.videoCodecs)}
        ${codecSelect(type, 'audioCodec', 'Audio codec', state.options.audioCodec, format?.audioCodecs)}
        ${numberField(type, 'maxHeight', 'Max height', state.options.maxHeight, presetPlaceholder(config.catalog, state.preset, type, state.targetFormat, 'maxHeight', '720'))}
        ${numberField(type, 'framerate', 'FPS', state.options.framerate, '30')}
        <div class="grid grid-cols-2 gap-3">
          ${numberField(type, 'videoBitrate', 'Video kbps', state.options.videoBitrate, '2500')}
          ${audioSupportsBitrate ? numberField(type, 'audioBitrate', 'Audio kbps', state.options.audioBitrate, '128') : ''}
        </div>
      </div>
    `;
  }
  if (effectKey === 'audio') {
    return `<div class="grid gap-4">${numberField(type, 'audioBitrate', 'Audio kbps', state.options.audioBitrate, '192')}</div>`;
  }
  return `
    <div class="grid gap-4">
      ${numberField(type, 'maxWidth', 'Max width', state.options.maxWidth, '1280')}
      ${numberField(type, 'quality', 'Quality %', state.options.quality, presetPlaceholder(config.catalog, state.preset, type, state.targetFormat, 'quality', '86'))}
    </div>
  `;
}

function numberField(type: MediaType, key: string, text: string, value: unknown, placeholder: string) {
  return `
    <label class="space-y-1.5">
      <span class="text-[11px] font-bold uppercase tracking-wider text-slate-500">${text}</span>
      <input class="field" type="number" min="0" data-option="${type}:${key}" value="${value || ''}" placeholder="${placeholder}" />
    </label>
  `;
}

function codecSelect(type: MediaType, key: 'videoCodec' | 'audioCodec', text: string, value: unknown, codecs?: FormatOption['videoCodecs']) {
  if (!codecs?.length) return '';
  const selected = String(value || '');
  return `
    <label class="space-y-1.5">
      <span class="text-[11px] font-bold uppercase tracking-wider text-slate-500">${text}</span>
      <select class="field" data-option="${type}:${key}">
        <option value="" ${selected === '' ? 'selected' : ''}>Auto (recommended)</option>
        ${codecs.map((codec) => `<option value="${codec.id}" ${selected === codec.id ? 'selected' : ''}>${escapeHTML(codec.label)}</option>`).join('')}
      </select>
    </label>
  `;
}

function fileRow(item: FileItem) {
  const icon = item.type === 'video' ? 'file-video' : item.type === 'audio' ? 'file-audio' : item.type === 'image' ? 'file-image' : 'file-warning';
  const progress = item.progress;
  const percent = progress?.phase === 'uploading' ? progress.uploadPercent : progress?.convertPercent || 0;
  
  let statusColor = 'text-slate-500';
  let badgeColor = 'badge';
  if (progress?.phase === 'finished') {
    statusColor = 'text-emerald-600';
    badgeColor = 'badge badge-success';
  } else if (progress?.phase === 'error') {
    statusColor = 'text-red-600';
  } else if (progress?.phase === 'queued' || progress?.phase === 'converting' || progress?.phase === 'uploading') {
    statusColor = 'text-brand-600';
    badgeColor = 'badge badge-brand';
  }

  const status = progress?.message || (item.type === 'unknown' ? 'Unsupported' : 'Ready to convert');
  
  return `
    <article class="panel group relative overflow-hidden rounded-2xl p-5 transition-all hover:border-slate-300/80">
      <div class="relative z-10 flex flex-wrap items-center justify-between gap-6">
        <div class="flex min-w-0 flex-1 items-center gap-4">
          <div class="flex h-12 w-12 shrink-0 items-center justify-center rounded-xl bg-slate-50 text-slate-400 group-hover:bg-white group-hover:text-brand-500 transition-colors border border-transparent group-hover:border-brand-100 shadow-sm">
            <i data-lucide="${icon}" class="h-6 w-6"></i>
          </div>
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2">
              <h3 class="truncate font-bold text-slate-900">${escapeHTML(item.file.name)}</h3>
              <span class="${badgeColor} uppercase tracking-tighter text-[10px]">${item.type}</span>
            </div>
            <div class="mt-1 flex items-center gap-3 text-[11px] font-bold uppercase tracking-wider">
              <span class="text-slate-400">${humanBytes(item.file.size)}</span>
              <span class="h-1 w-1 rounded-full bg-slate-200"></span>
              <span class="${statusColor}">${escapeHTML(status)}</span>
            </div>
          </div>
        </div>
        
        <div class="flex items-center gap-2">
          ${item.controller && progress?.phase !== 'finished' && progress?.phase !== 'error' && progress?.phase !== 'canceled' ? `
            <div class="flex items-center gap-1">
              <button class="btn btn-ghost h-10 w-10 p-0 rounded-full" data-pause="${item.id}" title="Pause"><i data-lucide="pause" class="h-4 w-4"></i></button>
              <button class="btn btn-ghost h-10 w-10 p-0 rounded-full" data-resume="${item.id}" title="Resume"><i data-lucide="play" class="h-4 w-4"></i></button>
              <button class="btn btn-ghost h-10 w-10 p-0 rounded-full text-red-500 hover:bg-red-50" data-cancel="${item.id}" title="Cancel"><i data-lucide="x" class="h-4 w-4"></i></button>
            </div>
          ` : `
            <button class="btn btn-ghost h-10 w-10 p-0 rounded-full text-slate-400 hover:text-red-500 hover:bg-red-50" data-remove="${item.id}" title="Remove">
              <i data-lucide="trash-2" class="h-4 w-4"></i>
            </button>
          `}
        </div>
      </div>
      
      ${progress && progress.phase !== 'finished' && progress.phase !== 'error' ? `
        <div class="mt-5 space-y-2">
          <div class="progress-track">
            <div class="progress-fill shadow-[0_0_8px_rgba(83,109,250,0.4)]" style="width:${percent}%"></div>
          </div>
        </div>
      ` : ''}
      
      ${preview(progress, item.type)}
    </article>
  `;
}

function preview(progress: UploadProgress | undefined, type: FileItem['type']) {
  if (!progress?.downloadUrl || progress.phase !== 'finished') return '';
  const url = progress.downloadUrl;
  const lower = url.toLowerCase();
  
  let content = '';
  if (type === 'video') {
    content = '';
  } else if (lower.includes('.mp3') || lower.includes('.wav') || lower.includes('.ogg') || lower.includes('.flac')) {
    content = `<audio class="w-full h-10" controls src="${url}"></audio>`;
  } else if (lower.includes('.mp4') || lower.includes('.webm') || lower.includes('.mov')) {
    content = `<video class="max-h-80 w-full rounded-xl bg-slate-900 shadow-inner" controls src="${url}"></video>`;
  } else {
    content = `<img class="max-h-80 w-full rounded-xl border border-slate-100 object-contain bg-slate-50 shadow-inner" src="${url}" alt="Converted preview" />`;
  }

  return `
    <div class="mt-6 flex flex-col gap-4 border-t border-slate-100 pt-6">
      ${content ? `<div class="relative overflow-hidden">
        ${content}
      </div>` : ''}
      <div class="flex items-center justify-end gap-3">
        <a class="btn btn-primary shadow-lg shadow-brand-100" href="${url}" download>
          <i data-lucide="download" class="h-4 w-4"></i>
          Download Result
        </a>
      </div>
    </div>
  `;
}

function downloadButton(url: string) {
  return `<a class="btn btn-primary w-fit" href="${url}" download><i data-lucide="download" class="h-4 w-4"></i> Download</a>`;
}

function renderLogin(config: AppConfig, message = '') {
  appRoot.innerHTML = authShell('Login', `
    ${message ? `<p class="rounded-xl bg-amber-50 p-4 text-xs font-semibold text-amber-800 border border-amber-100/50 mb-4">${message}</p>` : ''}
    <form id="login-form" class="grid gap-4">
      <label class="space-y-1.5">
        <span class="text-[11px] font-bold uppercase tracking-wider text-slate-500">Email</span>
        <input class="field" type="email" name="email" required placeholder="name@example.com" />
      </label>
      <label class="space-y-1.5">
        <span class="text-[11px] font-bold uppercase tracking-wider text-slate-500">Password</span>
        <input class="field" type="password" name="password" required placeholder="••••••••" />
      </label>
      <button class="btn btn-primary mt-2 shadow-lg shadow-brand-100" type="submit">
        <i data-lucide="log-in" class="h-4 w-4"></i> 
        Sign In
      </button>
    </form>
    ${config.setupNeeded ? `
      <div class="mt-6 border-t border-slate-100 pt-6 text-center">
        <a class="text-xs font-bold text-brand-600 hover:text-brand-700 transition-colors" href="/setup">Create first admin account</a>
      </div>
    ` : ''}
  `);
  document.querySelector<HTMLFormElement>('#login-form')?.addEventListener('submit', async (event) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget as HTMLFormElement);
    await login(String(form.get('email')), String(form.get('password')));
    window.location.href = '/admin';
  });
  activateIcons();
}

function renderSetup(config: AppConfig) {
  if (!config.setupNeeded) {
    window.location.href = '/';
    return;
  }
  appRoot.innerHTML = authShell('Create first admin', `
    <form id="setup-form" class="grid gap-4">
      <label class="space-y-1.5">
        <span class="text-[11px] font-bold uppercase tracking-wider text-slate-500">Setup token</span>
        <input class="field" name="setupToken" required placeholder="Enter token from server logs" />
      </label>
      <label class="space-y-1.5">
        <span class="text-[11px] font-bold uppercase tracking-wider text-slate-500">Email</span>
        <input class="field" type="email" name="email" required placeholder="admin@example.com" />
      </label>
      <label class="space-y-1.5">
        <span class="text-[11px] font-bold uppercase tracking-wider text-slate-500">Password</span>
        <input class="field" type="password" name="password" required minlength="8" placeholder="••••••••" />
      </label>
      <button class="btn btn-primary mt-2 shadow-lg shadow-brand-100" type="submit">
        <i data-lucide="shield-check" class="h-4 w-4"></i> 
        Create admin
      </button>
    </form>
  `);
  document.querySelector<HTMLFormElement>('#setup-form')?.addEventListener('submit', async (event) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget as HTMLFormElement);
    await setup(String(form.get('email')), String(form.get('password')), String(form.get('setupToken')));
    window.location.href = '/login';
  });
  activateIcons();
}

function authShell(title: string, content: string) {
  return `
    <main class="flex min-h-screen items-center justify-center p-6 bg-slate-50">
      <div class="w-full max-w-[400px] space-y-8">
        <div class="flex flex-col items-center text-center">
          <div class="flex h-12 w-12 items-center justify-center rounded-2xl bg-brand-600 text-white shadow-xl shadow-brand-200 mb-4">
            <i data-lucide="layers" class="h-7 w-7"></i>
          </div>
          <h2 class="text-2xl font-black tracking-tight text-slate-900">CloudConv</h2>
          <p class="mt-1 text-sm font-medium text-slate-500">Fast, local media conversion</p>
        </div>
        
        <div class="panel rounded-3xl p-8 shadow-xl shadow-slate-200/60">
          <h1 class="mb-6 text-xl font-bold text-slate-900">${title}</h1>
          ${content}
        </div>
        
        <div class="text-center">
          <a href="/" class="text-xs font-bold uppercase tracking-widest text-slate-400 hover:text-brand-600 transition-colors">← Back to Converter</a>
        </div>
      </div>
    </main>
  `;
}

function topbar(config: AppConfig) {
  return `
    <header class="flex flex-wrap items-center justify-between gap-6">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-xl bg-brand-600 text-white shadow-lg shadow-brand-200">
          <i data-lucide="layers" class="h-6 w-6"></i>
        </div>
        <div>
          <a href="/" class="text-xl font-black tracking-tight text-slate-900">CloudConv</a>
          <p class="text-[10px] font-bold uppercase tracking-widest text-slate-400">Media Engine v2</p>
        </div>
      </div>
      <nav class="flex items-center gap-3">
        ${config.auth.user?.role === 'admin' ? `
          <a class="btn btn-secondary h-10" href="/admin">
            <i data-lucide="layout-dashboard" class="h-4 w-4 text-slate-400"></i>
            Admin
          </a>
        ` : ''}
        ${config.auth.user ? `
          <div class="flex items-center gap-1 rounded-full bg-slate-100 pl-4 pr-1 py-1 border border-slate-200">
            <span class="text-xs font-bold text-slate-600">${escapeHTML(config.auth.user.email)}</span>
            <button id="logout" class="btn btn-ghost h-8 w-8 p-0 rounded-full hover:bg-white" title="Logout">
              <i data-lucide="log-out" class="h-3.5 w-3.5 text-slate-400"></i>
            </button>
          </div>
        ` : `
          <a class="btn btn-secondary h-10" href="/login">
            <i data-lucide="user" class="h-4 w-4 text-slate-400"></i>
            Sign In
          </a>
        `}
      </nav>
    </header>
  `;
}

function label(value: string) {
  return value.slice(0, 1).toUpperCase() + value.slice(1);
}

function escapeHTML(value: string) {
  return value.replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#039;' }[char] || char));
}

export function activateIcons() {
  createIcons({ 
    icons: { 
      Upload, Settings, LogIn, ShieldCheck, Download, RefreshCw, XCircle, Pause, Play, FileVideo, FileAudio, FileImage, Activity,
      Plus, Layers, LayoutDashboard, LogOut, User, Trash2, X, FileWarning, Video, Music, Image, Settings2, AlertCircle, Files,
      Zap, UploadCloud, Clock, UserMinus, UserPlus, History, Filter, Shield, Edit3, UserCheck, UserX, ChevronLeft, ChevronRight, ArrowLeft,
      RotateCcw, Save, Info, HardDrive, Cpu, ShieldAlert, Copy, SearchX, ExternalLink, PauseCircle, Check
    } 
  });
  document.querySelector('#logout')?.addEventListener('click', async () => {
    await logout();
    window.location.href = '/';
  });
}
