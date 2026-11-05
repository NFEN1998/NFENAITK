# Project User Instruction Memory

This file records user instructions, preferences, and teachings for reference in future interactions.

## Format

### User Instruction Entry
User instruction entries should follow this format:

[User Instruction Summary]
- Date: [YYYY-MM-DD]
- Context: [Mentioned scenario or time]
- Instructions:
  - [Content of user teaching or instruction, described line by line]

### Project Knowledge Entry
Entries discovered by the Agent during task execution should follow this format:

[Project Knowledge Summary]
- Date: [YYYY-MM-DD]
- Context: Discovered by Agent while performing [specific task description]
- Category: [Operations & Deployment|Build Methods|Testing Methods|Troubleshooting & Debugging|Workflow & Collaboration|Environment Configuration]
- Instructions:
  - [Specific knowledge points, described line by line]

## Deduplication Strategy
- Before adding a new entry, check for similar or identical instructions.
- If a duplicate is found, skip the new entry or merge it with the existing one.
- When merging, update the context or date information.
- This helps avoid redundant entries and keeps the memory file tidy.

## Entries

[Project Knowledge Summary]
- Date: 2026-09-29
- Context: Discovered by Agent while migrating the Go rewrite (/workspace/zerror-go) from SQLite to PostgreSQL + Redis
- Category: Operations & Deployment
- Instructions:
  - Go service root is /workspace/zerror-go (module `zerror`); build with `go build -o zerror .`
  - Connection settings come from env vars: `DATABASE_URL` (PostgreSQL DSN) and `REDIS_URL`; both override config.json
  - Run: `DATABASE_URL="postgres://zerror:zerror@127.0.0.1:5432/zerror?sslmode=disable" REDIS_URL="redis://127.0.0.1:6379/0" go run . -data-dir /tmp/zerror-pg -port 38081`
  - Old data import: `go run . migrate-sqlite -data-dir <dir> -src <old/database.db>`
  - CGO is required (gojieba for Chinese tokenization and go-sqlite3 for the importer), so `CGO_ENABLED=0` builds fail
  - Local test environment uses PostgreSQL 15 and Redis installed via apt; start with `service postgresql start` and `service redis-server start`
  - PostgreSQL folds unquoted identifiers to lowercase, so sequence lookups must use `pg_get_serial_sequence('folders','id')`

[Project Knowledge Summary]
- Date: 2026-11-05 (merged with 2026-10-06 entry)
- Context: Discovered by Agent while running integration tests for /workspace/nfentik-go
- Category: Testing Methods
- Instructions:
  - Integration tests for internal/server and internal/store share the same PostgreSQL database and Redis, and `go test ./...` runs packages in parallel, so the two packages clobber each other's users/logs and fail intermittently (errors like "用户不存在" / "令牌无效")
  - Always run tests serially with `cd /workspace/nfentik-go && go test -p 1 ./...`
  - Override the test database with the `TEST_DATABASE_URL` env var when a throwaway database is available
  - The environment resets wipe apt-installed services: if `service postgresql status` reports "unrecognized service", reinstall with `DEBIAN_FRONTEND=noninteractive apt-get install -y postgresql redis-server`, start both services, then recreate the test role/db: `runuser -u postgres -- psql -c "CREATE USER zerror WITH PASSWORD 'zerror';" -c "CREATE DATABASE zerror OWNER zerror;"` (default test DSN is postgres://zerror:zerror@127.0.0.1:5432/zerror)
  - Sandbox storage is slow under sustained write load (~4-5k row updates/sec on a local PostgreSQL, fsync ~1.6ms); migration/rehash benchmarks measured here are a worst case and do not reflect production hardware

[Project Knowledge Summary]
- Date: 2026-11-04 (merged with 2026-10-25 entry)
- Context: Discovered by Agent while packaging nfentik-go release builds for Linux and Windows
- Category: Environment Configuration
- Instructions:
  - The base Go toolchain at `/usr/local/go` may be older than required (e.g. 1.25.6); `nfentik-go/go.mod` declares `go 1.27.3` and `GOTOOLCHAIN=auto` downloads it on first build
  - `proxy.golang.org` and `go.dev` are unreachable from this environment (connection EOF); always build with `GOPROXY="https://goproxy.cn,direct"` so toolchain and module downloads succeed
  - Windows cross-builds need `gcc-mingw-w64-x86-64` and `g++-mingw-w64-x86-64`; the environment may reset, reinstall via `DEBIAN_FRONTEND=noninteractive apt-get install -y` if `x86_64-w64-mingw32-gcc` is missing
  - Release packaging: `bash scripts/packaging/build.sh` (Linux tar.gz) and `bash scripts/packaging/build-windows.sh` (Windows zip) inside nfentik-go; artifacts land in `nfentik-go/dist/` with sha256 files; run builds serially in background terminals (peak ~1.5 GiB each)
