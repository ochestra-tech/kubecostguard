# File: Makefile
# Variables
BINARY_NAME=kubecostguard
DOCKER_IMAGE=yourdockerhub/kubecostguard
DOCKER_TAG=latest
GO_VERSION=1.21

# Build commands
.PHONY: build
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BINARY_NAME) ./cmd/kubecostguard

.PHONY: build-local
build-local:
	go build -o $(BINARY_NAME) ./cmd/kubecostguard

.PHONY: clean
clean:
	go clean
	rm -f $(BINARY_NAME)
	rm -rf tmp/

# Development commands
.PHONY: run
run:
	go run ./cmd/kubecostguard --config=config.yaml

.PHONY: run-dev
run-dev:
	air -c .air.toml

# Testing commands
.PHONY: test
test:
	go test -v ./...

.PHONY: test-coverage
test-coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

.PHONY: test-integration
test-integration:
	go test -v -tags=integration ./...

# Docker commands
.PHONY: docker-build
docker-build:
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

.PHONY: docker-build-dev
docker-build-dev:
	docker build -f Dockerfile.dev -t $(DOCKER_IMAGE):dev .

.PHONY: docker-push
docker-push:
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)

.PHONY: docker-run
docker-run:
	docker run -p 8080:8080 --rm $(DOCKER_IMAGE):$(DOCKER_TAG)

# Kubernetes commands
.PHONY: k8s-deploy
k8s-deploy:
	kubectl apply -k deployments/kustomize/base

.PHONY: k8s-deploy-dev
k8s-deploy-dev:
	kubectl apply -k deployments/kustomize/overlays/dev

.PHONY: k8s-deploy-prod
k8s-deploy-prod:
	kubectl apply -k deployments/kustomize/overlays/production

.PHONY: k8s-undeploy
k8s-undeploy:
	kubectl delete -k deployments/kustomize/base

# Helm commands
.PHONY: helm-template
helm-template:
	helm template kubecostguard deployments/helm/kubecostguard

.PHONY: helm-install
helm-install:
	helm install kubecostguard deployments/helm/kubecostguard

.PHONY: helm-upgrade
helm-upgrade:
	helm upgrade kubecostguard deployments/helm/kubecostguard

.PHONY: helm-uninstall
helm-uninstall:
	helm uninstall kubecostguard

# Code quality commands
.PHONY: lint
lint:
	golangci-lint run

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

# Database migration commands
.PHONY: db-migrate
db-migrate:
	migrate -path ./migrations -database "$(DATABASE_URL)" up

.PHONY: db-migrate-down
db-migrate-down:
	migrate -path ./migrations -database "$(DATABASE_URL)" down

# Documentation commands
.PHONY: docs
docs:
	swag init -g ./cmd/kubecostguard/main.go -o ./docs/swagger

# Security scanning
.PHONY: scan
scan:
	trivy fs --exit-code 1 --severity HIGH,CRITICAL .

.PHONY: scan-docker
scan-docker:
	trivy image --exit-code 1 --severity HIGH,CRITICAL $(DOCKER_IMAGE):$(DOCKER_TAG)

# Setup commands
.PHONY: setup
setup:
	go mod download
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/cosmtrek/air@latest
	go install github.com/swaggo/swag/cmd/swag@latest
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# Help command
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build              - Build binary for Linux"
	@echo "  build-local        - Build binary for current OS"
	@echo "  clean              - Clean build artifacts"
	@echo "  run                - Run the application locally"
	@echo "  run-dev            - Run with hot-reloading"
	@echo "  test               - Run unit tests"
	@echo "  test-coverage      - Run tests with coverage"
	@echo "  test-integration   - Run integration tests"
	@echo "  docker-build       - Build Docker image"
	@echo "  docker-run         - Run Docker container"
	@echo "  k8s-deploy         - Deploy to Kubernetes"
	@echo "  k8s-deploy-dev     - Deploy to dev environment"
	@echo "  k8s-deploy-prod    - Deploy to production"
	@echo "  helm-install       - Install with Helm"
	@echo "  lint               - Run linter"
	@echo "  fmt                - Format code"
	@echo "  setup              - Setup development environment"
