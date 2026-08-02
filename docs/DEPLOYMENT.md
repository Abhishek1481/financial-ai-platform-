# Deployment guide

Three ways to run this platform, in increasing order of how much
infrastructure they need — each one is a real, working step, not a
placeholder for the next:

1. **Local processes** — two `go run`/`python -m` invocations, in-memory
   stores. Fastest inner loop; what every phase of this project was
   actually verified against.
2. **Docker Compose** — the same two services, containerized, plus real
   Redis/Postgres/Prometheus/Grafana.
3. **Kubernetes on AWS (EKS)** — Terraform provisions the cluster and
   supporting infrastructure, `kubectl` deploys onto it.

Each step's own README has the full detail; this page is the map between
them and what changes at each transition.

## 1. Local processes

```bash
# terminal 1
cd ml-service
python -m venv .venv && .venv/Scripts/pip install -e ".[dev]"   # Windows; .venv/bin/pip on macOS/Linux
make proto-python   # from the repo root, with the venv active
.venv/Scripts/python -m app.server

# terminal 2
cd gateway-go
go mod download
make proto-go        # from the repo root
go run ./cmd/gateway
```

`gateway-go` on `:8080`, `ml-service` on `:50051` (gRPC only — no public
HTTP port). See [`gateway-go/README.md`](../gateway-go/README.md) and
[`ml-service/README.md`](../ml-service/README.md) for every config
variable and what it defaults to. Both services' user/document/job state
is in-memory — nothing survives a restart.

## 2. Docker Compose

```bash
docker compose up --build
```

Adds real `redis` (gateway-go's search cache and conversation memory
switch to it automatically — see [`docker/README.md`](../docker/README.md)),
`postgres` (provisioned, not yet consumed by the application code — see
that same README for exactly what that means), and `prometheus`/`grafana`
wired to the dashboard from Phase 14
([`docs/monitoring/`](monitoring/README.md)).

**Caveat, stated plainly**: this repository's own development environment
has no Docker daemon, so `docker-compose.yml` and both Dockerfiles were
authored and reviewed carefully but never run here — `.github/workflows/
docker-build.yml` is what actually builds them on every push (see
[`../.github/workflows/`](../.github/workflows/)), which is the real
verification, not this guide's word for it.

## 3. Kubernetes on AWS

```bash
# provision the cluster and supporting infrastructure
cd terraform/environments/dev
cp terraform.tfvars.example terraform.tfvars   # fill in documents_bucket_name
terraform init && terraform apply

# point kubectl at it
aws eks update-kubeconfig --name financial-ai-platform-dev --region us-east-1

# deploy
kubectl apply -k k8s/overlays/dev
```

What changes going from Compose to this:

- **Shared storage becomes a real constraint.** Compose's two containers
  share a host filesystem for free; Kubernetes needs an explicit
  `ReadWriteMany` PVC for the same reason (see
  [`k8s/README.md`](../k8s/README.md)) — that requirement disappears
  entirely once `LocalObjectStore` is replaced with the S3 bucket
  Terraform already provisions (`terraform output documents_bucket_name`),
  which is the natural next step, not yet done.
- **`ml-service` runs a single replica**, deliberately — `FaissVectorStore`
  has no story for two processes sharing one index file. Scaling it needs
  an OpenSearch-backed implementation first.
- **IAM is real.** Each service's Kubernetes `ServiceAccount` can assume a
  scoped IAM role (IRSA) for S3/Secrets Manager access — provisioned by
  `terraform/modules/eks`, not yet wired into a `ServiceAccount` manifest
  (see `terraform/README.md`'s "What this does and doesn't wire up yet").

**Same caveat as step 2**: no `terraform`, `aws`, or `kubectl` binary
exists in this development environment either.
`.github/workflows/terraform-validate.yml` and `k8s-validate.yml` are
what actually validate these on every push.

## Configuration reference

Every environment variable either service reads, with its default, lives
in that service's own `.env.example` — `gateway-go/.env.example` and
`ml-service/.env.example` — not duplicated here, so there's exactly one
place each one can go stale.

## API reference

[`docs/openapi.yaml`](openapi.yaml) — the full REST surface `gateway-go`
exposes, structurally validated with `openapi-spec-validator` (not
auto-generated from the handlers, so treat it as documentation kept in
sync by hand, not a contract the code is generated from). View it
rendered at [editor.swagger.io](https://editor.swagger.io) by pasting the
file contents in, or with any local Swagger UI/Redoc instance pointed at
this file.
