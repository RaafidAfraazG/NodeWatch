# Reproducible demo

This workflow sends **simulated data**. It does not inspect disk capacity or create disk pressure on the computer running the demo.

Prometheus is part of the Compose stack. It scrapes the collector over the internal network and is published only on `127.0.0.1:9090`.

## Start the services

Create an ignored `.env` from `.env.example` and replace all three password placeholders. For a quick alert demonstration, set:

```sh
NODEWATCH_ALERT_EVALUATION_INTERVAL=1s
NODEWATCH_ALERT_FOR=0s
```

Then start the dependencies and collector:

```sh
docker compose config --quiet
docker compose up --build -d
until curl --fail --silent http://localhost:8080/readyz; do sleep 1; done
until curl --fail --silent http://localhost:9090/-/ready; do sleep 1; done
```

Expected readiness response shape:

```json
{"status":"ready"}
```

## Submit simulated data and inspect it

Run the included Go generator. Its default filesystem usage is 96%, above the default critical threshold.

```sh
go run ./cmd/demo --url http://localhost:8080/api/v1/metrics
curl http://localhost:8080/api/v1/hosts/demo-simulated-host
curl -G http://localhost:8080/api/v1/hosts/demo-simulated-host/metrics \
  --data-urlencode "from=2026-01-01T00:00:00Z" \
  --data-urlencode "to=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
sleep 3
curl 'http://localhost:8080/api/v1/alerts?state=firing&limit=20'
curl --silent http://localhost:8080/metrics | grep '^nodewatch_'
curl --silent 'http://localhost:9090/api/v1/query?query=nodewatch_ingestions_total'
```

The host response contains a status and latest snapshot. The alert response contains an `alerts` array; look for a `filesystem_usage` alert with `severity` `critical` and `state` `firing`.

## Evidence checklist for Prompt 5

- A successful `/readyz` response from a real Compose run.
- A simulated ingestion response and host/history API responses.
- A firing `filesystem_usage` alert from the controlled snapshot.
- Collector `/metrics` output and the corresponding Prometheus query result.
- Container status and logs from the same execution environment.

Stop the stack with `docker compose down`. This preserves the named MySQL and Redis volumes. Use `docker compose down -v` only for an intentionally disposable demo environment.
