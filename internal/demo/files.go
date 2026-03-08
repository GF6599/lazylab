package demo

import (
	"github.com/GF6599/lazylab/internal/gitlab"
)

func demoTree(_ int, path string) []gitlab.TreeNode {
	switch path {
	case "": // root
		return []gitlab.TreeNode{
			{Path: "cmd", Name: "cmd", Type: "tree", Mode: "040000"},
			{Path: "internal", Name: "internal", Type: "tree", Mode: "040000"},
			{Path: "pkg", Name: "pkg", Type: "tree", Mode: "040000"},
			{Path: ".gitlab-ci.yml", Name: ".gitlab-ci.yml", Type: "blob", Mode: "100644"},
			{Path: "Dockerfile", Name: "Dockerfile", Type: "blob", Mode: "100644"},
			{Path: "README.md", Name: "README.md", Type: "blob", Mode: "100644"},
			{Path: "go.mod", Name: "go.mod", Type: "blob", Mode: "100644"},
			{Path: "go.sum", Name: "go.sum", Type: "blob", Mode: "100644"},
		}
	case "cmd":
		return []gitlab.TreeNode{
			{Path: "cmd/server", Name: "server", Type: "tree", Mode: "040000"},
		}
	case "cmd/server":
		return []gitlab.TreeNode{
			{Path: "cmd/server/main.go", Name: "main.go", Type: "blob", Mode: "100644"},
		}
	case "internal":
		return []gitlab.TreeNode{
			{Path: "internal/handler", Name: "handler", Type: "tree", Mode: "040000"},
			{Path: "internal/service", Name: "service", Type: "tree", Mode: "040000"},
		}
	case "internal/handler":
		return []gitlab.TreeNode{
			{Path: "internal/handler/handler.go", Name: "handler.go", Type: "blob", Mode: "100644"},
			{Path: "internal/handler/handler_test.go", Name: "handler_test.go", Type: "blob", Mode: "100644"},
		}
	case "internal/service":
		return []gitlab.TreeNode{
			{Path: "internal/service/service.go", Name: "service.go", Type: "blob", Mode: "100644"},
		}
	case "pkg":
		return []gitlab.TreeNode{
			{Path: "pkg/config", Name: "config", Type: "tree", Mode: "040000"},
		}
	case "pkg/config":
		return []gitlab.TreeNode{
			{Path: "pkg/config/config.go", Name: "config.go", Type: "blob", Mode: "100644"},
		}
	default:
		return nil
	}
}

func demoFileContent(_ int, path string) string {
	content, ok := fileContents[path]
	if !ok {
		return "// placeholder: no demo content for " + path
	}
	return content
}

var fileContents = map[string]string{
	"cmd/server/main.go": `package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"acme-corp/project/internal/handler"
	"acme-corp/project/pkg/config"
)

func main() {
	cfg := config.MustLoad()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	mux := http.NewServeMux()
	handler.Register(mux, logger)

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		logger.Info("server starting", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutCtx)
	logger.Info("server stopped")
}
`,
	"internal/handler/handler.go": `package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Register mounts all HTTP routes on the given mux.
func Register(mux *http.ServeMux, logger *slog.Logger) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		logger.Info("listing projects", "remote", r.RemoteAddr)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]string{"alpha", "beta", "gamma"})
	})
}
`,
	"internal/handler/handler_test.go": `package handler_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"acme-corp/project/internal/handler"
)

func TestHealthz(t *testing.T) {
	mux := http.NewServeMux()
	handler.Register(mux, slog.Default())

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}
`,
	"README.md": `# Project

A lightweight API service built with Go's standard library.

## Quick Start

` + "```" + `bash
go run ./cmd/server
` + "```" + `

## Configuration

| Variable       | Default      | Description            |
|----------------|--------------|------------------------|
| LISTEN_ADDR    | :8080        | HTTP listen address    |
| LOG_LEVEL      | info         | Log verbosity          |
| DATABASE_URL   | (required)   | PostgreSQL DSN         |

## Testing

` + "```" + `bash
go test -race ./...
` + "```" + `
`,
	".gitlab-ci.yml": `stages:
  - build
  - test
  - lint
  - deploy

variables:
  GOFLAGS: "-buildvcs=false"
  CGO_ENABLED: "0"

build:
  stage: build
  image: golang:1.24-alpine
  script:
    - go build ./...
  artifacts:
    paths:
      - bin/

test:
  stage: test
  image: golang:1.24-alpine
  script:
    - go test -race -count=1 -coverprofile=coverage.out ./...
  artifacts:
    reports:
      coverage_report:
        coverage_format: cobertura
        path: coverage.out

lint:
  stage: lint
  image: golangci/golangci-lint:v2.0
  script:
    - golangci-lint run ./...

deploy:
  stage: deploy
  image: bitnami/kubectl:latest
  script:
    - kubectl set image deployment/api api=$CI_REGISTRY_IMAGE:$CI_COMMIT_SHA
  when: manual
  only:
    - main
`,
	"go.mod": `module acme-corp/project

go 1.24

require (
	github.com/stretchr/testify v1.9.0
)
`,
	"Dockerfile": `FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.* ./
RUN go mod download
COPY . .
RUN go build -o /bin/server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /bin/server /usr/local/bin/server
EXPOSE 8080
ENTRYPOINT ["server"]
`,
	"internal/service/service.go": `package service

import (
	"context"
	"fmt"
)

// Store defines the persistence contract for the service layer.
type Store interface {
	Get(ctx context.Context, id string) (map[string]any, error)
	List(ctx context.Context, limit, offset int) ([]map[string]any, error)
}

// Service implements business logic on top of a Store.
type Service struct {
	store Store
}

// New returns a Service backed by the given store.
func New(store Store) *Service {
	return &Service{store: store}
}

// GetByID retrieves a single record.
func (s *Service) GetByID(ctx context.Context, id string) (map[string]any, error) {
	if id == "" {
		return nil, fmt.Errorf("id must not be empty")
	}
	return s.store.Get(ctx, id)
}
`,
	"pkg/config/config.go": `package config

import "os"

// Config holds runtime settings.
type Config struct {
	ListenAddr  string
	LogLevel    string
	DatabaseURL string
}

// MustLoad reads config from environment or panics.
func MustLoad() Config {
	return Config{
		ListenAddr:  envOr("LISTEN_ADDR", ":8080"),
		LogLevel:    envOr("LOG_LEVEL", "info"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
`,
}
