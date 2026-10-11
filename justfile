# zy-stats-go command runner. Requires just 1.54 or later.
# Recipes that run until Ctrl-C carry [continue]: just lets the program handle
# the signal and succeeds when it exits cleanly.
# Run `just` to list the recipes by group.

# Build metadata from git; the fallbacks keep recipes working outside a git
# checkout or before the first commit.

version := `git describe --tags --always 2>/dev/null || echo dev`
commit := `git rev-parse --short HEAD 2>/dev/null || echo unknown`
build_date := `git log -1 --format=%cI 2>/dev/null || true`
ldflags := "-s -w -X main.version=" + version + " -X main.buildDate=" + build_date

# golangci-lint release used by lint-docker; it must be built with Go 1.27 or later.

golangci_lint_version := "v2.14.0"

# Docker Compose with the production override.

prod_compose := "docker compose -f compose.yaml -f compose.prod.yaml"

# List the recipes
[private]
default:
    @just --list --unsorted

# Build the binary into bin/zy-stats-go with the version and commit date from git
[group('build')]
build:
    go build -trimpath -ldflags "{{ ldflags }}" -o bin/zy-stats-go ./cmd/zy-stats-go

# Cross-compile release binaries for every supported target into dist/
[group('build')]
dist:
    #!/usr/bin/env sh
    set -eu
    rm -rf dist && mkdir -p dist
    for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
      os=${target%/*}; arch=${target#*/}
      ext=""; [ "$os" = windows ] && ext=".exe"
      CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "{{ ldflags }}" \
        -o "dist/zy-stats-go-{{ version }}-$os-$arch$ext" ./cmd/zy-stats-go
    done
    (cd dist && shasum -a 256 zy-stats-go-* > SHA256SUMS)
    ls -l dist

# Build and run the program from source, passing the arguments through
# (exec instead of go run, so Ctrl-C reaches the program and its exit code is kept)
[continue]
[group('build')]
[positional-arguments]
run *args:
    go build -o bin/zy-stats-go-run ./cmd/zy-stats-go
    exec bin/zy-stats-go-run "$@"

# Start the fake FWA505 on 127.0.0.1:8080 for manual end-to-end runs
[continue]
[group('build')]
[positional-arguments]
fakemodem *args:
    go build -o bin/fakemodem ./internal/zyxel/zyxeltest/cmd/fakemodem
    exec bin/fakemodem "$@"

# Run the format check, vet, lint and tests (before every commit)
[group('check')]
check: fmt-check vet lint test

# Run the tests with the race detector
[group('check')]
test:
    go test -race -count=1 ./...

# Run the tests and print per-function coverage
[group('check')]
test-cover:
    go test -race -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out

# Rewrite the golden files under testdata/stats from the current code (review the diff)
[group('check')]
golden:
    go test -count=1 ./internal/stats -update

# Run golangci-lint
[group('check')]
lint:
    golangci-lint run

# Run golangci-lint and the format check with the pinned release in Docker (no local install)
[group('check')]
lint-docker:
    docker run --rm -v "$PWD":/src:ro -w /src -e GOFLAGS=-buildvcs=false golangci/golangci-lint:{{ golangci_lint_version }} \
      sh -c 'golangci-lint run ./... && golangci-lint fmt --diff'

# Format the Go sources (gofmt, gofumpt, goimports via golangci-lint)
[group('check')]
fmt:
    golangci-lint fmt

# Report formatting differences without writing changes
[group('check')]
fmt-check:
    golangci-lint fmt --diff

# Run go vet
[group('check')]
vet:
    go vet ./...

# Tidy go.mod and go.sum and fail if they change
[group('check')]
tidy:
    go mod tidy
    git diff --exit-code -- go.mod go.sum

# Show the running Compose services
[group('docker')]
docker-ps:
    {{ prod_compose }} ps

# Build the production image (version, revision and commit date from git)
[group('docker')]
docker-build:
    VERSION={{ version }} VCS_REF={{ commit }} BUILD_DATE={{ build_date }} {{ prod_compose }} build

# Build the production image for linux/amd64 and linux/arm64 without pushing
[group('docker')]
docker-build-multiarch:
    docker buildx build --platform linux/amd64,linux/arm64 --target prod \
      --build-arg VERSION={{ version }} --build-arg VCS_REF={{ commit }} --build-arg BUILD_DATE={{ build_date }} .

# Start the production container in the foreground
[continue]
[group('docker')]
docker-up-attached:
    {{ prod_compose }} up

# Start the production container in the background
[group('docker')]
docker-up-detached:
    {{ prod_compose }} up -d

# Run the production image with --log in the foreground until Ctrl-C, removing the container on exit
[continue]
[group('docker')]
docker-start-log:
    {{ prod_compose }} run --rm zy-stats-go --log

# Print the version baked into the locally built production image (no .env needed; run docker-build first)
[group('docker')]
docker-version:
    docker run --rm --pull never "$({{ prod_compose }} config --images)" --version

# Stop and remove the production container
[group('docker')]
docker-down:
    {{ prod_compose }} down

# Rebuild the production image and restart the container
[group('docker')]
docker-rebuild: docker-down docker-build docker-up-detached

# Print the production logs once
[group('docker')]
docker-logs-once:
    -{{ prod_compose }} logs

# Follow the production logs
[continue]
[group('docker')]
docker-logs:
    -{{ prod_compose }} logs -f

# Remove the production containers, local images, volumes and orphans
[confirm('Remove the production containers, local images and volumes?')]
[group('docker')]
docker-clean:
    {{ prod_compose }} down --rmi local --volumes --remove-orphans

# Pull the Go toolchain image used by the development container
[group('docker-dev')]
docker-dev-pull:
    docker compose pull

# Start the development container in the foreground
[continue]
[group('docker-dev')]
docker-dev-up-attached:
    docker compose up

# Start the development container in the background
[group('docker-dev')]
docker-dev-up-detached:
    docker compose up -d

# Run the program from source with --log in the foreground until Ctrl-C, removing the container on exit
[continue]
[group('docker-dev')]
docker-dev-start-log:
    docker compose run --rm zy-stats-go sh -c 'go build -o /tmp/zy-stats-go ./cmd/zy-stats-go && exec /tmp/zy-stats-go --log'

# Stop and remove the development container
[group('docker-dev')]
docker-dev-down:
    docker compose down

# Pull the toolchain image again and restart the development container
[group('docker-dev')]
docker-dev-rebuild: docker-dev-down docker-dev-pull docker-dev-up-detached

# Open a shell in the running development container
[group('docker-dev')]
docker-dev-shell:
    docker compose exec zy-stats-go /bin/sh

# Run go vet and the tests in the development container
[group('docker-dev')]
docker-dev-check:
    docker compose run --rm zy-stats-go sh -c "go vet ./... && go test -count=1 ./..."

# Check formatting in the development container (gofmt only; golangci-lint is not in the image)
[group('docker-dev')]
docker-dev-fmt-check:
    docker compose run --rm zy-stats-go sh -c 'test -z "$(gofmt -l cmd internal)" || { gofmt -l cmd internal; exit 1; }'

# Format the Go sources in the development container
[group('docker-dev')]
docker-dev-fmt-write:
    docker compose run --rm zy-stats-go gofmt -w cmd internal

# Print the development logs once
[group('docker-dev')]
docker-dev-logs-once:
    -docker compose logs

# Follow the development logs
[continue]
[group('docker-dev')]
docker-dev-logs:
    -docker compose logs -f

# Remove the development containers, images, volumes and orphans
[confirm('Remove the development containers, images and volumes?')]
[group('docker-dev')]
docker-dev-clean:
    docker compose down --rmi local --volumes --remove-orphans

# Print today's log file
[group('logs')]
show-logs:
    @cat "logs/$(date +%Y-%m-%d).log" 2>/dev/null || echo "No log file found for today."

# Delete log files older than 30 days
[confirm('Delete the log files older than 30 days?')]
[group('logs')]
logs-clean:
    find logs -name '*.log' -mtime +30 -delete
