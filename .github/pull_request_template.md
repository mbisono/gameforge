## What this changes

<!-- What the change does and why. Link the issue if there is one ("Fixes #12"). -->

## How it was tested

<!-- Tests added or run, and what you tried in the app (system, tab, file formats). -->

## Checklist

- [ ] `go vet ./...` and `go test ./...` pass, and the code is formatted with `gofmt`.
- [ ] New format code, parsers and anything that writes or deletes files has a test.
- [ ] New user-visible strings are in `internal/i18n/messages.go` in all five languages (or the PR says which ones are missing).
- [ ] No keys, no games or ROMs and no personal paths anywhere, also not in test data.
- [ ] No code copied from GPL projects.
