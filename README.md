CloudConv
=========

CloudConv is a self-hosted media converter built with Go, SQLite, FFmpeg, and a Vite/TypeScript frontend. It supports public anonymous conversion by default, optional account-gated uploads, resumable chunked uploads, persistent jobs, and an admin panel for settings, users, logs, and stats.

Features
--------

- Smart single-page converter for video, audio, and image files.
- Batch uploads with shared settings per media type.
- Resumable chunked uploads, defaulting to a configurable 10 GiB max file size.
- SQLite persistence for settings, users, sessions, uploads, jobs, and events.
- Optional login/account management with first-run admin setup.
- Admin panel at `/admin` for queue stats, logs, users, and runtime settings.
- FFmpeg-backed conversion progress and persistent downloads by job ID.
- Legacy multipart upload endpoints kept for compatibility.

Supported Outputs
-----------------

- Video: MP4, WebM, MOV, AVI, MKV, GIF
- Audio: MP3, WAV, OGG, FLAC
- Image: JPG, PNG, WebP, BMP, TIFF

Development
-----------

Prerequisites:

- Go 1.25+
- Node 24+
- FFmpeg and FFprobe in `PATH`

Install frontend dependencies and build assets:

```bash
npm install
npm run build
```

Run the server:

```bash
go run main.go
```

Open `http://localhost:3000`.

On first run, create the first admin at `/setup`. Set `CLOUDCONV_SETUP_TOKEN` yourself in production; if it is missing, the server prints a one-time generated setup token to logs.

Configuration
-------------

Environment variables:

- `CLOUDCONV_ADDR=:3000`
- `CLOUDCONV_DB_PATH=/app/data/cloudconv.db`
- `CLOUDCONV_UPLOAD_DIR=/app/uploads`
- `CLOUDCONV_CONVERTED_DIR=/app/converted`
- `CLOUDCONV_SETUP_TOKEN`
- `CLOUDCONV_COOKIE_SECURE`
- `CLOUDCONV_TRUST_PROXY`

Runtime settings such as public uploads, upload size, queue depth, rate limits, worker count, conversion timeout, and retention are editable in the admin panel.

Docker
------

```bash
docker compose up --build
```

Persistent data is stored in:

- `./data`
- `./uploads`
- `./converted`

Tests
-----

```bash
npm test
npm run build
go test ./...
```

The default Go tests include small FFmpeg smoke conversions through both the resumable upload API and the legacy multipart API.
