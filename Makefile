SHELL := /bin/bash

.PHONY: configure run dev frontend test build

configure:
	go mod tidy

run:
	go run main.go

dev:
	@set -euo pipefail; \
	mkdir -p data uploads converted; \
	echo "Starting CloudConv API on http://localhost:3000"; \
	echo "Starting Vite UI on http://localhost:5173"; \
	echo "First-run setup token: dev-setup-token"; \
	if [ ! -d node_modules ]; then npm install; fi; \
	CLOUDCONV_ADDR=:3000 \
	CLOUDCONV_DB_PATH=data/dev-cloudconv.db \
	CLOUDCONV_UPLOAD_DIR=uploads \
	CLOUDCONV_CONVERTED_DIR=converted \
	CLOUDCONV_SETUP_TOKEN=dev-setup-token \
	go run . & \
	api_pid=$$!; \
	trap 'kill $$api_pid 2>/dev/null || true' EXIT INT TERM; \
	npm run dev

frontend:
	npm install
	npm run build

test:
	npm test
	npm run build
	go test ./...

build:
	docker build --platform linux/amd64 -t kirari04/cloudconv:latest .
