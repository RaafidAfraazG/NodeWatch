# Contributing

NodeWatch has completed its initial five-milestone build. Keep changes scoped and discuss wire-contract changes before implementing downstream features.

Use Go 1.22 or newer. The agent uses the standard library; the collector has pinned MySQL/Redis drivers and test-only miniredis. Run:

```sh
make fmt
make test
make lint
make build
```

Without Make, run `gofmt -w` on changed Go files, `go test -timeout 60s ./...`, `go vet ./...`, and `go build ./...`. Native Linux verification is required for kernel-facing changes. On another OS, at least cross-compile with `GOOS=linux GOARCH=amd64 go build ./...`; cross-compilation does not establish live Linux runtime behavior. Storage changes also require the real-service suite in [docs/TESTING.md](docs/TESTING.md). Report skips and environment limitations explicitly.

Prefer small, readable packages and table-driven parser tests with representative fixtures. Test cancellation, bounded retries, malformed input, and missing metrics when touching those paths. Do not turn missing metrics into zero or infer host data that was not actually collected. Keep Linux-specific code build-tagged. Avoid new dependencies unless they solve a clear problem.

Before opening a PR, describe the behavior changed, relevant tests actually run, and any platform limitations. Do not include binaries, credentials, personal data, fabricated screenshots, or unsupported performance claims. Keep example payloads explicitly labeled as examples. The module path is `github.com/RaafidAfraazG/NodeWatch`; preserve it in new internal imports.

Contributions are made under the MIT license in this repository.
