# overlays

Kustomize overlays for per-environment manifest patches — `dev/` (a local
cluster: 1 replica, smaller resource requests, debug logging, reflection
on) and `prod/` (real image registry references, real ingress
host + TLS). Apply with `kubectl apply -k k8s/overlays/dev` (or `prod`).
No `staging/` overlay yet — add one the same way once there's an actual
staging cluster to target; `dev` and `prod` are the two this project
needs today.
