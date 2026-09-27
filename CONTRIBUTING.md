# Contributing to At The Rack

Thanks for your interest in improving At The Rack! It's a small, self-hosted
Go app, so contributing is deliberately low-ceremony.

## Getting set up

You need [Go 1.25+](https://go.dev/dl/). No Node or frontend build step is
required to run the app itself.

```sh
git clone https://github.com/jwald3/attherack && cd attherack
go run . -seed-demo -db dev.db   # first run: a dev database with fake data
go run . -db dev.db              # later runs: reuse it
```

Then open <http://localhost:8080>. See the [README](README.md) for the full
tour, configuration, and how the coach works.

## Before you open a pull request

Please make sure these all pass locally — CI runs the same checks:

```sh
go test ./...          # unit tests
go vet ./...           # static checks
gofmt -l .             # should print nothing; run `gofmt -w .` to fix
go build ./...         # it compiles
```

If your change touches the UI or a coach tool, run the end-to-end suite too:

```sh
cd e2e
npm ci && npx playwright install chromium   # once
npm test
```

The e2e tests need no API key and make no network calls — they run against
`e2e/fake-claude.mjs`, a deterministic stand-in for the Anthropic API. See the
README's "End-to-end tests" section for how to script it.

## A few conventions

- **Keep the stack small.** No JavaScript build step, no new heavy
  dependencies without a good reason. The app is Go standard library +
  [HTMX](https://htmx.org) + [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite).
- **Templates and static files are embedded** with `go:embed`, so restart the
  app after editing them.
- **Dependencies point one way:** `server` and `coach` use `store` and
  `exercise`, never the reverse.
- **Add a test** next to the code you change (`*_test.go`), and cover new UI or
  coach behavior in `e2e/` where it makes sense.
- **Never commit database files** (`*.db`, WAL files, backups) — they hold
  personal data and possibly an API key. They're git-ignored.

## Reporting bugs and requesting features

Open an issue. For bugs, the template asks for steps to reproduce, what you
expected, your OS, and how you're running the app (`go run`, a built binary, or
Docker) — that context makes a huge difference.

## Security

Please don't file security issues in the public tracker. See
[SECURITY.md](SECURITY.md).
