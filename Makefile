.PHONY: frontend backend build

frontend:
	cd frontend && pnpm install && pnpm build

backend: frontend
	go build -o calnode ./cmd/calnode

build: backend
