# base

Base Kubernetes manifests shared across all environments: `Deployment`/
`Service` for `gateway-go` and `ml-service`, a `HorizontalPodAutoscaler`
and `Ingress` for `gateway-go`, `ConfigMap`/`Secret` pairs for both
services' env-driven config, a shared `PersistentVolumeClaim` for uploaded
documents, a per-service `PersistentVolumeClaim` for ml-service's vector
store, and `redis`/`postgres` (see `/k8s/README.md` for why each is
provisioned the way it is — in particular, why Postgres isn't consumed by
gateway-go yet). Tied together by `kustomization.yaml`; see
`../overlays/` for the per-environment patches on top of this.
