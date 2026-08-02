# k8s

Kubernetes manifests (Phase 17) — `base/` (plain manifests + a
`kustomization.yaml` tying them together) and `overlays/{dev,prod}/`
(per-environment patches). See `base/`'s and `overlays/`'s own READMEs for
what each holds.

**Verification note**: authored and reviewed carefully, but not validated
against an actual cluster or even `kubectl`/`kustomize` themselves — no
Docker daemon, `kubectl`, or `kustomize` binary is available in this
development environment (same constraint `/docker-compose.yml` already
flags for Phase 16, one layer further up the stack). Every YAML file here
was parsed with a real YAML parser and every embedded JSON6902 patch block
was parsed and structurally checked (list of `{op, path, value}`), which
catches syntax errors — it does not catch a `kubectl apply --dry-run`-only
class of mistake (a patch path that doesn't exist on the target resource,
an invalid field value, a missing `imagePullSecrets` for a private
registry). Run `kustomize build k8s/overlays/dev` (or `kubectl kustomize`,
built in since kubectl 1.14) against a real cluster before trusting this
further.

## Apply

```bash
kubectl apply -k k8s/overlays/dev   # or overlays/prod
```

## Architectural decisions specific to these manifests

**gateway-go and ml-service share a `ReadWriteMany` PVC (`documents-pvc`).**
`ingestion.Service` hands ml-service a `file://` URI, not the file's
bytes (see `gateway-go/README.md`'s "Storage URIs, not bytes, cross the
gRPC boundary") — in Docker Compose that "just works" because both
containers run on the same host; in Kubernetes it requires both
Deployments to mount the identical filesystem, which is what
`storage-pvc.yaml` sets up. This needs a StorageClass that actually
supports RWX (NFS, EFS, Azure Files) — the default StorageClass on many
local clusters (kind, a stock minikube) doesn't, and the PVC will sit
`Pending` until one that does is available. This exact requirement goes
away once `LocalObjectStore` is replaced with S3/MinIO — a URI crossing a
network boundary instead of a filesystem boundary needs no shared volume
at all.

**ml-service runs a single replica.** `FaissVectorStore` is a local,
non-distributed index with no story for two processes writing the same
file concurrently (see `vector_store.py`'s module docstring) — scaling
ml-service horizontally needs an OpenSearch-backed `VectorStore`
implementation first (the interface already anticipates this), not more
replicas of the current one.

**gateway-go's HPA scales on CPU only.** Request handling here is
I/O-bound on ml-service far more than CPU-bound (same reasoning
`ml-service/README.md` gives for choosing `grpc.aio`) — CPU is a
reasonable-enough autoscaling signal without needing a custom-metrics
adapter (queue depth, in-flight requests) this project has no wiring for.

**Redis runs on `emptyDir`, Postgres on a real PVC.** Losing Redis's data
on a pod restart costs the next request a cache miss or a re-sent
conversation history — never a correctness bug, the same reasoning
`MemoryCache`'s own doc comment gives for being fine to lose on restart.
Postgres, once actually wired up (see below), can't make that same
tradeoff.

**Postgres is provisioned but not consumed.** `auth.UserRepository`,
`ingestion.DocumentRepository`, and `ingestion.JobRepository` are already
written against interfaces specifically so a Postgres-backed
implementation is a self-contained follow-up (see each package's own
`Memory*Repository` doc comment) — that swap hasn't happened yet in this
codebase, so `gateway-go` doesn't read `postgres-secret.yaml`'s
credentials today. The StatefulSet exists so the follow-up has
somewhere to point at.

## Secrets

`gateway-go-secret.yaml`, `ml-service-secret.yaml`, and
`postgres-secret.yaml` all ship with placeholder values, safe only
because `config.Load()` itself refuses to start with
`GATEWAY_ENVIRONMENT=production` if the JWT secret or admin password
still match their insecure defaults. Replace them — or better, generate
them via your cluster's actual secret manager (Sealed Secrets, External
Secrets Operator, SOPS) instead of committing plaintext — before applying
to anything but a throwaway cluster.
