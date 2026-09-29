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
