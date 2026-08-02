# Monitoring (Phase 14)

Both services expose Prometheus metrics on a port separate from real
traffic — `gateway-go` on `GATEWAY_METRICS_PORT` (default 9090, since
Phase 4), `ml-service` on `ML_SERVICE_METRICS_PORT` (default 9091, new
this phase).

```bash
curl http://localhost:9090/metrics   # gateway-go
curl http://localhost:9091/metrics   # ml-service
```

## What's instrumented

**gateway-go** (`internal/metrics`, `internal/mlclient/interceptor.go`,
and self-instrumenting `cache`/`ratelimit`/`conversation` packages):

- `gateway_http_requests_total{method,route,status}`, `gateway_http_request_duration_seconds{method,route}`
- `gateway_mlclient_requests_total{method,status}`, `gateway_mlclient_request_duration_seconds{method}` — every gRPC call gateway-go makes to ml-service
- `gateway_cache_requests_total{result="hit"|"miss"}`
- `gateway_rate_limit_rejections_total`
- `gateway_conversation_sessions_active`, `gateway_conversation_sessions_pruned_total`

**ml-service** (`app/observability.py`, a single `grpc.aio.ServerInterceptor`
applied uniformly rather than instrumented by hand per servicer):

- `ml_service_grpc_requests_total{method,status}`, `ml_service_grpc_request_duration_seconds{method}`

`prometheus.yml` in this directory is a ready-to-use scrape config for both
(`prometheus --config.file docs/monitoring/prometheus.yml` against
locally-running processes); it targets `localhost`, so update it once
Phase 16's Docker Compose stack gives both services real hostnames.

## Grafana

`grafana-dashboard.json` is importable as-is (Dashboards → Import → upload
JSON) once Grafana is pointed at a Prometheus scraping the above — request
rate/latency for both services, mlclient call health, cache hit ratio,
rate-limit rejections, and active conversation sessions. No Grafana
instance is deployed yet (that's Phase 16 too); this is the dashboard
definition ready for when one is.

## Request-ID log correlation (not full OpenTelemetry)

Full distributed tracing (OTel SDK, spans, an exporter, a collector like
Jaeger/Tempo) needs a tracing backend that isn't deployed until Phase 16
at the earliest. Rather than skip tracing-adjacent observability entirely
until then, every HTTP request gets a correlation ID
(`internal/middleware.RequestID`, echoed as the `X-Request-ID` response
header) that's forwarded to ml-service as gRPC metadata
(`internal/mlclient`'s client interceptor) and picked up there
(`app/observability.py` extracts it, `app/tracing.py` makes it available
to every log line via a contextvar + logging filter). The result: every
structured log line either service emits while handling one request
carries the same `request_id`, so `grep request_id=<id>` across both
services' logs reconstructs that request's full path — most of the
practical debugging value OTel targets, without the infrastructure
dependency. This ID is designed to slot directly into an OTel `trace_id`
field later; adopting the real SDK is additive, not a rewrite.
