import {
  Activity,
  Download,
  FileAudio,
  FileImage,
  FileVideo,
  LogIn,
  Pause,
  Play,
  RefreshCw,
  Settings,
  ShieldCheck,
  Upload,
  XCircle,
  createIcons
} from 'lucide';
import './styles.css';
import { loadConfig, type AppConfig } from './api';
import { login, logout, setup } from './auth';
import { detectMediaType, formatsFor, humanBytes, type MediaType } from './catalog';
import { renderAdmin } from './admin';
import { uploadAndConvert, type UploadController, type UploadProgress } from './upload';

type FileItem = {
  id: string;
  file: File;
  type: MediaType | 'unknown';
  progress?: UploadProgress;
  controller?: UploadController;
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
      await renderAdmin(appRoot);
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
  appRoot.innerHTML = `
    <main class="shell mx-auto flex w-full max-w-7xl flex-col gap-5 px-4 py-5">
      ${topbar(config)}
      <section class="grid gap-5 lg:grid-cols-[minmax(0,1fr)_23rem]">
        <div class="flex flex-col gap-4">
          <div id="dropzone" class="dropzone rounded-lg p-6">
            <div class="flex flex-col items-center gap-3 text-center">
              <div class="rounded-full bg-emerald-50 p-3 text-emerald-800"><i data-lucide="upload" class="h-7 w-7"></i></div>
              <div>
                <h1 class="text-2xl font-extrabold tracking-tight">Convert media files</h1>
                <p class="mt-1 max-w-xl text-sm text-slate-600">Drop video, audio, or image files. CloudConv detects each type and applies shared settings per group.</p>
              </div>
              <label class="btn btn-primary cursor-pointer">
                <i data-lucide="upload" class="h-4 w-4"></i>
                Select files
                <input id="file-input" class="sr-only" type="file" multiple />
              </label>
              <div class="text-xs font-semibold text-slate-500">Max upload ${humanBytes(Number(config.settings.max_upload_bytes || 0))}</div>
            </div>
          </div>
          <section id="file-list" class="flex flex-col gap-3"></section>
        </div>
        <aside id="options" class="flex flex-col gap-4"></aside>
      </section>
    </main>
  `;
  const dropzone = document.querySelector<HTMLElement>('#dropzone')!;
  const fileInput = document.querySelector<HTMLInputElement>('#file-input')!;
  const fileList = document.querySelector<HTMLElement>('#file-list')!;
  const options = document.querySelector<HTMLElement>('#options')!;

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
    renderOptions();
    renderFiles();
    activateIcons();
  }

  function renderOptions() {
    const presentTypes = new Set(files.map((item) => item.type).filter((type): type is MediaType => type !== 'unknown'));
    options.innerHTML = `
      <section class="panel rounded-lg p-4">
        <div class="mb-3 flex items-center justify-between gap-3">
          <h2 class="font-bold">Conversion settings</h2>
          <button id="start-all" class="btn btn-primary" type="button" ${files.length === 0 ? 'disabled' : ''}>
            <i data-lucide="refresh-cw" class="h-4 w-4"></i> Convert
          </button>
        </div>
        ${files.length === 0 ? '<p class="text-sm text-slate-600">Select files to show relevant formats and options.</p>' : ''}
        ${Array.from(presentTypes).map((type) => groupControls(type, states[type], config)).join('')}
        ${files.some((item) => item.type === 'unknown') ? '<p class="mt-3 rounded-md bg-amber-50 p-3 text-sm font-semibold text-amber-800">Unsupported files are shown in the list and will be skipped.</p>' : ''}
      </section>
    `;
    options.querySelectorAll<HTMLSelectElement>('[data-format]').forEach((select) => {
      select.addEventListener('change', () => {
        states[select.dataset.format as MediaType].targetFormat = select.value;
        renderAll();
      });
    });
    options.querySelectorAll<HTMLSelectElement>('[data-preset]').forEach((select) => {
      select.addEventListener('change', () => {
        states[select.dataset.preset as MediaType].preset = select.value;
      });
    });
    options.querySelectorAll<HTMLInputElement>('[data-option]').forEach((input) => {
      input.addEventListener('change', () => {
        const [type, key] = (input.dataset.option || '').split(':') as [MediaType, string];
        states[type].options[key] = input.type === 'checkbox' ? input.checked : Number(input.value || 0);
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
    fileList.innerHTML = files.length === 0 ? '' : files.map((item) => fileRow(item)).join('');
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
    files.filter((item) => item.type !== 'unknown' && !item.controller).forEach((item) => {
      const state = states[item.type as MediaType];
      item.controller = uploadAndConvert(
        item.file,
        {
          targetFormat: state.targetFormat,
          preset: state.preset,
          options: state.options
        },
        (progress) => {
          item.progress = progress;
          renderFiles();
          activateIcons();
        }
      );
    });
    renderFiles();
  }
  renderAll();
}

function defaultGroupStates(config: AppConfig): Record<MediaType, GroupState> {
  const first = (type: MediaType) => formatsFor(config.catalog, type)[0]?.id || 'mp4';
  return {
    video: { targetFormat: first('video'), preset: 'balanced', advanced: false, options: { loop: true } },
    audio: { targetFormat: first('audio'), preset: 'balanced', advanced: false, options: {} },
    image: { targetFormat: first('image'), preset: 'balanced', advanced: false, options: {} }
  };
}

function groupControls(type: MediaType, state: GroupState, config: AppConfig) {
  const formats = formatsFor(config.catalog, type);
  return `
    <div class="mt-4 rounded-lg border border-slate-200 p-3">
      <div class="mb-3 flex items-center justify-between">
        <h3 class="font-bold capitalize">${type}</h3>
        <button class="btn btn-secondary min-h-8 px-2 py-1 text-xs" data-advanced="${type}" type="button">
          <i data-lucide="settings" class="h-3.5 w-3.5"></i> Advanced
        </button>
      </div>
      <label class="block text-sm font-semibold text-slate-700">Target
        <select class="field mt-1" data-format="${type}">
          ${formats.map((format) => `<option value="${format.id}" ${state.targetFormat === format.id ? 'selected' : ''}>${format.label}</option>`).join('')}
        </select>
      </label>
      <label class="mt-3 block text-sm font-semibold text-slate-700">Preset
        <select class="field mt-1" data-preset="${type}">
          ${config.catalog.presets.map((preset) => `<option value="${preset}" ${state.preset === preset ? 'selected' : ''}>${label(preset)}</option>`).join('')}
        </select>
      </label>
      ${state.advanced ? advancedControls(type, state) : ''}
    </div>
  `;
}

function advancedControls(type: MediaType, state: GroupState) {
  if (type === 'video') {
    return `
      <div class="mt-3 grid gap-2">
        ${numberField(type, 'maxHeight', 'Max height', state.options.maxHeight, '720')}
        ${numberField(type, 'framerate', 'FPS', state.options.framerate, '30')}
        ${numberField(type, 'videoBitrate', 'Video kbps', state.options.videoBitrate, '2500')}
        ${numberField(type, 'audioBitrate', 'Audio kbps', state.options.audioBitrate, '128')}
        <label class="flex items-center gap-2 text-sm font-semibold"><input type="checkbox" data-option="${type}:loop" ${(state.options.loop ?? true) ? 'checked' : ''}/> Loop GIFs</label>
      </div>
    `;
  }
  if (type === 'audio') {
    return `<div class="mt-3">${numberField(type, 'audioBitrate', 'Audio kbps', state.options.audioBitrate, '192')}</div>`;
  }
  return `
    <div class="mt-3 grid gap-2">
      ${numberField(type, 'maxWidth', 'Max width', state.options.maxWidth, '1280')}
      ${numberField(type, 'quality', 'Quality', state.options.quality, '86')}
    </div>
  `;
}

function numberField(type: MediaType, key: string, text: string, value: unknown, placeholder: string) {
  return `<label class="block text-sm font-semibold text-slate-700">${text}<input class="field mt-1" type="number" min="0" data-option="${type}:${key}" value="${value || ''}" placeholder="${placeholder}" /></label>`;
}

function fileRow(item: FileItem) {
  const icon = item.type === 'video' ? 'file-video' : item.type === 'audio' ? 'file-audio' : item.type === 'image' ? 'file-image' : 'x-circle';
  const progress = item.progress;
  const percent = progress?.phase === 'uploading' ? progress.uploadPercent : progress?.convertPercent || 0;
  const status = progress?.message || (item.type === 'unknown' ? 'Unsupported' : 'Ready');
  return `
    <article class="panel rounded-lg p-4">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="flex min-w-0 gap-3">
          <div class="rounded-md bg-slate-100 p-2 text-slate-700"><i data-lucide="${icon}" class="h-5 w-5"></i></div>
          <div class="min-w-0">
            <h3 class="truncate font-bold">${escapeHTML(item.file.name)}</h3>
            <div class="mt-1 flex flex-wrap gap-2 text-xs font-semibold text-slate-500">
              <span>${humanBytes(item.file.size)}</span>
              <span class="badge">${item.type}</span>
              <span>${escapeHTML(status)}</span>
            </div>
          </div>
        </div>
        <div class="flex gap-2">
          ${item.controller && progress?.phase !== 'finished' ? `
            <button class="btn btn-secondary min-h-9 px-2" data-pause="${item.id}" title="Pause"><i data-lucide="pause" class="h-4 w-4"></i></button>
            <button class="btn btn-secondary min-h-9 px-2" data-resume="${item.id}" title="Resume"><i data-lucide="play" class="h-4 w-4"></i></button>
            <button class="btn btn-danger min-h-9 px-2" data-cancel="${item.id}" title="Cancel"><i data-lucide="x-circle" class="h-4 w-4"></i></button>
          ` : `<button class="btn btn-secondary min-h-9 px-2" data-remove="${item.id}" title="Remove"><i data-lucide="x-circle" class="h-4 w-4"></i></button>`}
        </div>
      </div>
      ${progress ? `<div class="mt-4 progress-track"><div class="progress-fill" style="width:${percent}%"></div></div>` : ''}
      ${preview(progress)}
    </article>
  `;
}

function preview(progress?: UploadProgress) {
  if (!progress?.downloadUrl || progress.phase !== 'finished') return '';
  const url = progress.downloadUrl;
  const lower = url.toLowerCase();
  if (lower.includes('.mp3') || lower.includes('.wav') || lower.includes('.ogg') || lower.includes('.flac')) {
    return `<div class="mt-4 flex flex-wrap items-center gap-3"><audio controls src="${url}"></audio>${downloadButton(url)}</div>`;
  }
  if (lower.includes('.mp4') || lower.includes('.webm') || lower.includes('.mov')) {
    return `<div class="mt-4 grid gap-3"><video class="max-h-72 rounded-md bg-black" controls src="${url}"></video>${downloadButton(url)}</div>`;
  }
  return `<div class="mt-4 grid gap-3"><img class="max-h-72 rounded-md border border-slate-200 object-contain" src="${url}" alt="Converted preview" />${downloadButton(url)}</div>`;
}

function downloadButton(url: string) {
  return `<a class="btn btn-primary w-fit" href="${url}" download><i data-lucide="download" class="h-4 w-4"></i> Download</a>`;
}

function renderLogin(config: AppConfig, message = '') {
  appRoot.innerHTML = authShell('Login', `
    ${message ? `<p class="rounded-md bg-amber-50 p-3 text-sm font-semibold text-amber-800">${message}</p>` : ''}
    <form id="login-form" class="grid gap-3">
      <label class="text-sm font-semibold">Email<input class="field mt-1" type="email" name="email" required /></label>
      <label class="text-sm font-semibold">Password<input class="field mt-1" type="password" name="password" required /></label>
      <button class="btn btn-primary" type="submit"><i data-lucide="log-in" class="h-4 w-4"></i> Login</button>
    </form>
    ${config.setupNeeded ? '<a class="text-sm font-bold text-emerald-800" href="/setup">Create first admin</a>' : ''}
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
    <form id="setup-form" class="grid gap-3">
      <label class="text-sm font-semibold">Setup token<input class="field mt-1" name="setupToken" required /></label>
      <label class="text-sm font-semibold">Email<input class="field mt-1" type="email" name="email" required /></label>
      <label class="text-sm font-semibold">Password<input class="field mt-1" type="password" name="password" required minlength="8" /></label>
      <button class="btn btn-primary" type="submit"><i data-lucide="shield-check" class="h-4 w-4"></i> Create admin</button>
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
    <main class="shell mx-auto flex min-h-screen w-full max-w-md flex-col justify-center gap-4 px-4 py-8">
      <a href="/" class="text-xl font-extrabold">CloudConv</a>
      <section class="panel rounded-lg p-5">
        <h1 class="mb-4 text-2xl font-extrabold">${title}</h1>
        ${content}
      </section>
    </main>
  `;
}

function topbar(config: AppConfig) {
  return `
    <header class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <a href="/" class="text-xl font-extrabold tracking-tight">CloudConv</a>
        <p class="text-sm font-medium text-slate-500">Self-hosted media conversion</p>
      </div>
      <nav class="flex flex-wrap gap-2">
        ${config.auth.user?.role === 'admin' ? '<a class="btn btn-secondary" href="/admin"><i data-lucide="activity" class="h-4 w-4"></i> Admin</a>' : ''}
        ${config.auth.user ? `<button id="logout" class="btn btn-secondary" type="button">${escapeHTML(config.auth.user.email)}</button>` : '<a class="btn btn-secondary" href="/login"><i data-lucide="log-in" class="h-4 w-4"></i> Login</a>'}
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

function activateIcons() {
  createIcons({ icons: { Upload, Settings, LogIn, ShieldCheck, Download, RefreshCw, XCircle, Pause, Play, FileVideo, FileAudio, FileImage, Activity } });
  document.querySelector('#logout')?.addEventListener('click', async () => {
    await logout();
    window.location.href = '/';
  });
}
