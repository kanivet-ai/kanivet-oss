.PHONY: help
help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: dev
dev: ## Run Electron app with frontend and backend hot-reload
	cd frontend && npm run dev

.PHONY: dev-backend
dev-backend: ## Run backend with hot-reload (air)
	cd backend && air

.PHONY: dev-frontend
dev-frontend: ## Run frontend dev server only
	cd frontend && npm start

.PHONY: build
build: build-backend build-frontend ## Build both backend and frontend

.PHONY: build-backend
build-backend: ## Build backend binary for current platform (development, with logging)
	cd backend && go build -ldflags="-s -w" -trimpath -o ../frontend/resources/kanivet-backend ./cmd/main.go
	chmod 755 frontend/resources/kanivet-backend

.PHONY: build-backend-prod
build-backend-prod: ## Build backend binary for current platform (production, no logging)
	cd backend && go build -tags production -ldflags="-s -w" -trimpath -o ../frontend/resources/kanivet-backend ./cmd/main.go
	chmod 755 frontend/resources/kanivet-backend

.PHONY: build-backend-darwin-amd64
build-backend-darwin-amd64: ## Build backend for macOS Intel (production, no logging)
	cd backend && GOOS=darwin GOARCH=amd64 go build -tags production -ldflags="-s -w" -trimpath -o ../frontend/resources/kanivet-backend-darwin-amd64 ./cmd/main.go
	chmod 755 frontend/resources/kanivet-backend-darwin-amd64

.PHONY: build-backend-darwin-arm64
build-backend-darwin-arm64: ## Build backend for macOS Apple Silicon (production, no logging)
	cd backend && GOOS=darwin GOARCH=arm64 go build -tags production -ldflags="-s -w" -trimpath -o ../frontend/resources/kanivet-backend-darwin-arm64 ./cmd/main.go
	chmod 755 frontend/resources/kanivet-backend-darwin-arm64

.PHONY: build-backend-linux-amd64
build-backend-linux-amd64: ## Build backend for Linux x64 (production, no logging)
	cd backend && GOOS=linux GOARCH=amd64 go build -tags production -ldflags="-s -w" -trimpath -o ../frontend/resources/kanivet-backend-linux-amd64 ./cmd/main.go
	chmod 755 frontend/resources/kanivet-backend-linux-amd64

.PHONY: build-backend-linux-arm64
build-backend-linux-arm64: ## Build backend for Linux ARM64 (production, no logging)
	cd backend && GOOS=linux GOARCH=arm64 go build -tags production -ldflags="-s -w" -trimpath -o ../frontend/resources/kanivet-backend-linux-arm64 ./cmd/main.go
	chmod 755 frontend/resources/kanivet-backend-linux-arm64

.PHONY: build-backend-windows-amd64
build-backend-windows-amd64: ## Build backend for Windows x64 (production, no logging)
	cd backend && GOOS=windows GOARCH=amd64 go build -tags production -ldflags="-s -w" -trimpath -o ../frontend/resources/kanivet-backend-windows-amd64.exe ./cmd/main.go

.PHONY: build-backend-all
build-backend-all: ## Build backend for all platforms
	$(MAKE) build-backend-darwin-amd64
	$(MAKE) build-backend-darwin-arm64
	$(MAKE) build-backend-linux-amd64
	$(MAKE) build-backend-linux-arm64
	$(MAKE) build-backend-windows-amd64

.PHONY: build-frontend
build-frontend: ## Build frontend for production
	cd frontend && npm run build

.PHONY: build-app
build-app: ## Build complete Electron app (backend + frontend)
	cd frontend && npm run build:app

.PHONY: dist
dist: ## Build distributable app for all platforms
	cd frontend && npm run dist

.PHONY: dist-mac
dist-mac: ## Build macOS distributable (universal binary)
	cd frontend && npm run dist:mac

.PHONY: dist-mac-arm64
dist-mac-arm64: build-backend-darwin-arm64 ## Build macOS distributable for Apple Silicon
	cd frontend && npm run dist:mac -- --arm64

.PHONY: dist-mac-x64
dist-mac-x64: build-backend-darwin-amd64 ## Build macOS distributable for Intel
	cd frontend && npm run dist:mac -- --x64

.PHONY: dist-win
dist-win: ## Build Windows distributable (x64)
	cd frontend && npm run dist:win

.PHONY: dist-win-x64
dist-win-x64: build-backend-windows-amd64 ## Build Windows distributable for x64
	cd frontend && npm run dist:win -- --x64

.PHONY: dist-linux
dist-linux: ## Build Linux distributable (x64)
	cd frontend && npm run dist:linux

.PHONY: dist-linux-x64
dist-linux-x64: build-backend-linux-amd64 ## Build Linux distributable for x64
	cd frontend && npm run dist:linux -- --x64

.PHONY: dist-linux-arm64
dist-linux-arm64: build-backend-linux-arm64 ## Build Linux distributable for ARM64
	cd frontend && npm run dist:linux -- --arm64

.PHONY: dist-all
dist-all: build-backend-all ## Build distributables for all platforms
	cd frontend && npm run dist

.PHONY: test
test: test-backend test-frontend ## Run all tests

.PHONY: test-frontend
test-frontend: ## Run frontend node-based tests
	cd frontend && npm test

.PHONY: test-backend
test-backend: ## Run backend tests
	cd backend && go test ./...

.PHONY: test-backend-verbose
test-backend-verbose: ## Run backend tests with verbose output
	cd backend && go test -v ./...

.PHONY: test-backend-coverage
test-backend-coverage: ## Run backend tests with coverage report
	cd backend && ./scripts/coverage.sh

.PHONY: test-backend-coverage-html
test-backend-coverage-html: ## Run backend tests and open coverage report in browser
	cd backend && ./scripts/coverage.sh && open coverage.html

.PHONY: lint
lint: lint-backend lint-frontend ## Lint both backend and frontend

.PHONY: lint-backend
lint-backend: ## Run golangci-lint on backend
	cd backend && ./scripts/lint.sh

.PHONY: lint-frontend
lint-frontend: ## Check frontend code formatting
	cd frontend && npm run prettier:check

.PHONY: lint-fix
lint-fix: lint-fix-backend lint-fix-frontend ## Fix linting issues in both backend and frontend

.PHONY: lint-fix-backend
lint-fix-backend: ## Auto-fix backend linting issues
	cd backend && golangci-lint run --fix

.PHONY: lint-fix-frontend
lint-fix-frontend: ## Auto-fix frontend code formatting
	cd frontend && npm run prettier

.PHONY: logs
logs: ## Tail both frontend and backend logs
	tail -f frontend/logs/frontend-server.log backend/logs/kanivet-backend.log

.PHONY: logs-frontend
logs-frontend: ## Tail frontend logs
	tail -f frontend/logs/frontend-server.log

.PHONY: logs-backend
logs-backend: ## Tail backend logs
	tail -f backend/logs/kanivet-backend.log

.PHONY: clean
clean: ## Clean build artifacts and logs
	rm -rf backend/bin backend/tmp
	rm -rf frontend/build frontend/dist
	rm -f frontend/resources/kanivet-backend*
	find . -name "*.map" -delete
	rm -f backend/logs/*.log frontend/logs/*.log 2>/dev/null || true

.PHONY: install
install: install-backend install-frontend ## Install all dependencies

.PHONY: install-backend
install-backend: ## Install backend dependencies
	cd backend && go mod download

.PHONY: install-frontend
install-frontend: ## Install frontend dependencies
	cd frontend && npm install

.PHONY: update-deps
update-deps: ## Update all dependencies
	cd backend && go get -u ./... && go mod tidy
	cd frontend && npm update

.PHONY: pre-commit
pre-commit: ## Run pre-commit on all files
	pre-commit run --all-files

.PHONY: pre-commit-install
pre-commit-install: ## Install pre-commit hooks
	pre-commit install

.PHONY: docker-build
docker-build: ## Build Docker image for current platform
	docker build -t kanivet:latest .

.PHONY: docker-build-multiarch
docker-build-multiarch: ## Build multi-arch Docker image (amd64, arm64)
	docker buildx build --platform linux/amd64,linux/arm64 -t kanivet:latest .

.PHONY: docker-build-push
docker-build-push: ## Build and push multi-arch Docker image
	docker buildx build --platform linux/amd64,linux/arm64 -t kanivet:latest --push .

.PHONY: docker-buildx-setup
docker-buildx-setup: ## Setup Docker buildx for multi-arch builds
	docker buildx create --name kanivet-builder --use || true
	docker buildx inspect --bootstrap

.PHONY: docker-run
docker-run: ## Run Docker container
	docker run -p 8080:8080 kanivet:latest
