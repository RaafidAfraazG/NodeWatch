# Testing milestone 3

## Portable checks

```sh
make fmt
go test -timeout 60s ./...
go vet ./...
go build ./...
```

The GitHub Actions workflow runs these checks on pushes and pull requests, then separately runs real MySQL and Redis service-container tests. See [ci.yml](../.github/workflows/ci.yml).

Unit tests cover fixture parsing, CPU I/O-wait math, numeric and configuration validation, strict JSON handling, HTTP status/error responses, request deadlines, agent-to-collector compatibility, duplicate/conflict responses, unknown/malformed and stale hosts, alert lifecycle/API behavior, cache reconciliation pagination/retry/cancellation, historical query bounds, readiness, and partial dependency failures. HTTP orchestration uses in-memory fakes. MySQL canonicalization tests do not establish actual SQL behavior. Redis unit tests use **miniredis**, an emulator, to execute Lua updates, exercise concurrent nanosecond ordering, simulate TTL expiration, refresh duplicates, and prevent stale completion from restoring presence.

## Real-service integration tests

`make test-integration` runs `go test -tags=integration -count=1 -timeout 60s ./internal/storage/... ./internal/collector`. It does not provision services. Each real-service test explicitly skips unless its integration address is configured. A green command containing skips is not evidence of database integration success. The collector integration test posts a snapshot through the real MySQL/Redis adapters, verifies duplicate behavior and history retrieval, waits for a real heartbeat expiry with polling, and verifies the resulting offline alert lifecycle.

Required MySQL variables:

- `NODEWATCH_INTEGRATION_MYSQL_ADDR=host:3306`
- `NODEWATCH_MYSQL_DATABASE=nodewatch_test` (name **must end in `_test`**)
- `NODEWATCH_MYSQL_USER` and `NODEWATCH_MYSQL_PASSWORD`

The test database must already exist and the test account must be able to create tables and read/write/delete its test data. Tests apply migration 1 twice, exercise concurrent inserts under one identity, assert exactly one durable record, reject conflicts, preserve nanosecond distinctions, prevent latest-state regression, and check bounded history pagination and missing hosts. They create unique test host IDs and delete only those hosts/records afterward. They do not truncate production data.

Required Redis variables:

- `NODEWATCH_INTEGRATION_REDIS_ADDR=host:6379`
- `NODEWATCH_REDIS_USERNAME` and `NODEWATCH_REDIS_PASSWORD` if needed

The Redis integration test uses database 0 with uniquely named test keys. It verifies real Lua execution, newest-state preservation, actual heartbeat expiration, and duplicate refresh. It deletes only its own two keys. Use a disposable test service.

## Isolated Compose integration environment

`docker-compose.test.yml` provisions separate test-only services and runs the integration suite inside a Go build container. It has no production named volumes or published ports. Supply the three password variables through the ignored `.env` or environment, as for the main Compose file.

```sh
docker compose -p nodewatch-test -f docker-compose.test.yml config --quiet
docker compose -p nodewatch-test -f docker-compose.test.yml up --build --abort-on-container-exit --exit-code-from tests
# Remove only this disposable test project's services and anonymous volumes:
docker compose -p nodewatch-test -f docker-compose.test.yml down -v
```

This is separate from `docker-compose.yml`, which runs the collector, Prometheus, and persistent MySQL/Redis data. The test image uses the Dockerfile's `build` stage, containing source and the Go toolchain.

## Verification limits

The implementation environment is Windows with a portable Go toolchain. No Docker CLI/engine, Redis server, or MySQL server is available, so real-service integration and Compose configuration/image/health execution must be verified elsewhere. The official Prometheus client was downloaded and its unit tests pass, but no live database result, container health, or Prometheus scrape is claimed. Cross-building for Linux checks compilation only. Tests are not benchmarks or durability certification.
