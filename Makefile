.PHONY: frontend backend build i18n-coverage

frontend:
	cd frontend && pnpm install && pnpm build

backend: frontend
	go build -o calnode ./cmd/calnode

build: backend

# Per-language translation coverage, for both catalogues. Informational; the tests in
# internal/i18n are the gate.
i18n-coverage:
	go run ./cmd/i18n-coverage
