# zy-stats-go

Fetch Zyxel's stats from the CLI in a pretty way.

Built in Go as a single static binary (no runtime, no browser). It logs in to the router's web interface the same way the browser does, polls the cellular status and either prints a coloured report in the terminal or appends one JSON line per poll to a daily log file. This project was originally written with Node.js: [zy-stats](https://github.com/ailequal/zy-stats).

> **AI usage disclosure**: This codebase was written with LLM-based tools. It is reviewed and tested, but bugs and odd patterns are possible. Review before relying on it in production.

## Device support

The following Zyxel router model is supported:

- Zyxel Nebula FWA505, reached over HTTPS at its default address `https://192.168.1.1` with the default management settings.

Other models and setups are not supported.

## Local

To use it locally on your machine, first make sure to have Go installed and, optionally, [just](https://github.com/casey/just) to run the recipes.

No browser and no other runtime are needed on the host: the login is plain HTTPS.

Here below are listed the basic steps to install and run the program:

```bash
just build            # builds ./bin/zy-stats-go with the version from git
# or, without just:
go build -o bin/zy-stats-go ./cmd/zy-stats-go

cp .env.example .env  # fill in your router credentials

./bin/zy-stats-go        # shows stats nicely formatted into the terminal
./bin/zy-stats-go --log  # appends one JSON line per poll to logs/YYYY-MM-DD.log
./bin/zy-stats-go --once # one report, then exit (handy in scripts)
./bin/zy-stats-go -c     # one compact line per poll, scrolling instead of clearing the screen
./bin/zy-stats-go --server-url 'https://192.168.1.1' --username admin --password 'password' --interval 5 # without filling the .env file
just run --interval 5 # or run from source like this

# handy alias
alias zy-stats-go="cd ~/path-to-repos/zy-stats-go && ./bin/zy-stats-go"
```

Once a release is published, `go install github.com/ailequal/zy-stats-go/cmd/zy-stats-go@latest` installs the binary into `$(go env GOPATH)/bin`, and the release page carries prebuilt binaries for Linux, macOS and Windows.

## Docker

The Docker setup builds the binary in a Go image and copies it into a distroless image with no shell, running as a non-root user, so that no local Go installation is required on the host. Two modes are available: **production** (lean, binary baked in) and **development** (Go toolchain, source mounted from the host).

### Production

The production image contains only the static binary. Use it to run the program without any local setup.

Logs land in the same `./logs/` directory as the non-Docker setup and survive container restarts and rebuilds. The container runs as uid 65532: on Linux hosts make `./logs` writable by that user, either with `chown 65532 logs` or by setting `ZY_UID` and `ZY_GID` to your own ids in the shell or in `.env`.

```bash
cp .env.example .env  # fill in your router credentials
just docker-build
just docker-up-detached
```

### Development

The development container builds and runs the program from source. The project directory is bind-mounted into the container, so source code changes are picked up on the next start; the module and build caches live in named volumes. Only pull again after the Go image is updated.

```bash
cp .env.example .env  # fill in your router credentials
just docker-dev-pull
just docker-dev-up-detached
```

## CLI arguments

- `--server-url`, `-s`: the URL of the Zyxel router (`https://...`, no trailing slash).
- `--username`, `-u`: the username to access the Zyxel router.
- `--password`, `-p`: the password to access the Zyxel router.
- `--interval`, `-i`: the interval in seconds to fetch the stats (required, at least 1).
- `--log`, `-l`: log the stats into a file in JSON format.
- `--compact`, `-c`: print one line per poll instead of the full report (combines with `--log`).
- `--once`: fetch the stats once, then exit.
- `--logs-dir`: the directory for the daily log files (default: `./logs`).
- `--log-level`: diagnostics on stderr, `debug`, `info`, `warn` or `error` (default: `info`).
- `--debug`: same as `--log-level debug`; also prints every HTTP exchange with passwords, cookies and sessionkeys redacted.
- `--version`, `-V` (prints the version, the Go toolchain, the build date and the platform) and `--help`, `-h`.

Every option except `--log`, `--compact`, `--once`, `--debug`, `--version` and `--help` can also be set in `.env` or in the environment with the `ZY_` prefix (`ZY_SERVER_URL`, `ZY_USERNAME`, `ZY_PASSWORD`, `ZY_INTERVAL`, `ZY_LOGS_DIR`, `ZY_LOG_LEVEL`). Flags win over the environment, which wins over `.env`.

If you have [just](https://github.com/casey/just) installed, run `just` to list all available recipes and run them quickly.

## Development

The code follows the standard Go layout: the command in `cmd/zy-stats-go/`, every other package under `internal/`, tests next to the code, and shared fixtures in `testdata/` (see [`testdata/README.md`](testdata/README.md)).

```bash
just check        # format check, vet, lint and tests: run before every commit
just test         # tests with the race detector
just golden       # rewrite the expected outputs in testdata/stats after an intended change
just lint-docker  # lint and format check with the pinned golangci-lint, no local install
```

`just check` needs [golangci-lint](https://golangci-lint.run/docs/welcome/install/local/) v2.14.0 or later (earlier releases cannot read a Go 1.27 module); `just lint-docker` runs that exact version in Docker instead.

Versions follow [Semantic Versioning](https://semver.org/) and are recorded in [`CHANGELOG.md`](CHANGELOG.md). A release is a git tag with the `v` prefix that Go modules require, for example `git tag -a v1.0.0 -m "v1.0.0"`; `just build`, `just dist` and `just docker-build` read the version from that tag.

## Resources

- [zy-stats](https://github.com/ailequal/zy-stats)
- [Zyxel Nebula FWA505](https://www.zyxel.com/global/en/products/mobile-broadband/nebula-5g-nr-indoor-router-nebula-fwa505)
- [miononno.it/router/zyxel-fwa-505](https://miononno.it/router/zyxel-fwa-505)
