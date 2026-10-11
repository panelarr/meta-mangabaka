# Contributing

Thanks for helping. This page covers the development setup and how changes get in.

## Development setup

You need Go (the version in `go.mod`; the `go` command downloads it if yours is older) and `make`.

This plugin is built with the SDK from [meta](https://github.com/panelarr/meta). Until meta publishes tagged versions, `go.mod` points at a **sibling checkout** with a `replace` directive, so clone both repos next to each other:

```sh
git clone https://github.com/panelarr/meta
git clone https://github.com/panelarr/meta-mangabaka
cd meta-mangabaka
make setup   # installs pinned tools (golangci-lint, govulncheck) into ./bin
make check   # format check, vet, lint, govulncheck and tests: run this before every PR
make help    # lists every target
make docker  # builds the image, passing ../meta as a named build context
```

### No MangaBaka data in this repo

MangaBaka's data is CC BY-NC-SA 4.0. Never commit the dump, an extract, or real records, in code, tests, fixtures or docs. Tests build a **synthetic** SQLite database with made-up series that follows the dump's layout, and serve it from a local test server.

## Working conventions

- **Tasks:** everything runs through `make`.
- **Branches:** `<type>/<scope>-<slug>`, e.g. `feat/dump-download-resume`.
- **Commits and PR titles:** [Conventional Commits](https://www.conventionalcommits.org/): `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `perf`, `build`, `ci`. Add `!` for breaking changes. PRs are squash-merged, so the PR title becomes the commit message.
- **Reviews:** at least one approval and green checks before merge.
- **AI agents:** agents may open PRs and post reviews. Anything an agent posts from a person's account starts with a **⚠️ AI agent** line. Humans stay responsible for what they merge.
