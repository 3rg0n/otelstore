# Contributing to otelstore

Thanks for your interest. otelstore is a small, single-binary local OpenTelemetry
backend; contributions that keep it small, dependency-free, and easy to run are
very welcome.

## Ground rules

- **Stay single-binary and pure-Go.** No CGO. `CGO_ENABLED=0 go build ./...`
  must succeed — that is what lets otelstore ship as one static executable on
  macOS, Linux, and Windows. Don't add a dependency that pulls in a C toolchain.
- **Keep the store generic.** Only the flat correlation keys `run_id` and
  `job_id` are promoted to columns; everything else (including OpenTelemetry
  `gen_ai.*` attributes) is stored as generic JSON. Don't special-case attribute
  names in the store.
- **Small surface.** No UI, no metrics dashboards, no extra query languages.
  otelstore ingests OTLP and answers fixed queries; richer views belong in
  tools you point at it (e.g. Grafana).

## Development

```sh
# build and run the full gate before opening a PR
CGO_ENABLED=0 go build ./...
go vet ./...
go test ./...
```

If you have them installed, please also run the security/quality tools this
project gates on:

```sh
gosec ./...          # must be 0 issues
staticcheck ./...
govulncheck ./...
```

The repo is a Go workspace (`go.work`) that also contains `emit/` — Go and Rust
helper libraries for producing conformant OTLP spans (see
`docs/overlay-schema.md`).

## Pull requests

- Keep each PR focused on one change; include tests with real assertions.
- Update `CHANGELOG.md` under `[Unreleased]`.
- For decisions that are costly to reverse (storage engine, public API shape,
  security posture), add or reference an ADR in `docs/adr/`.
- Describe how you verified the change — ideally against the running binary, not
  just unit tests.

## Reporting issues

Open an issue with: what you ran (command + flags), what you expected, what
happened, and your OS/arch. For ingest problems, note the OTLP protocol
(`grpc` vs `http/protobuf`) and endpoint you used.

## License

By contributing, you agree that your contributions are licensed under the
project's [MIT License](LICENSE).
