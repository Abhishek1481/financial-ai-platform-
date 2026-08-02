# terraform

AWS infrastructure for running the Kubernetes manifests in `/k8s` on a
real EKS cluster (Phase 18) — `modules/` (reusable: networking, eks,
storage, database, secrets, observability) composed by
`environments/{dev,prod}/` root modules. See each subdirectory's own
README for what it holds.

**Verification note**: authored and reviewed carefully, but not validated
against `terraform validate`/`terraform plan`, or applied to a real AWS
account — no `terraform` or `aws` CLI is available in this development
environment (the same constraint `/docker-compose.yml` and `/k8s` already
flag, one layer further down the stack). What *was* checked: every `.tf`
file parses as valid HCL (via a real HCL2 parser, not hand-inspection),
and every module invocation in both `environments/dev/main.tf` and
`environments/prod/main.tf` supplies every variable that module declares
without a default (checked programmatically against each module's actual
`variables.tf`, not assumed). That catches syntax errors and "forgot to
pass a required variable" — it does not catch a `terraform plan`-only
class of mistake (an invalid AMI/instance-type combination, an IAM policy
that doesn't actually grant what it means to, a resource attribute that
doesn't exist on the provider version pinned in `versions.tf`). Run
`terraform init && terraform validate` in each `environments/*` directory
against a real AWS account/credentials before trusting this further.

## Apply

```bash
cd terraform/environments/dev
cp terraform.tfvars.example terraform.tfvars   # fill in documents_bucket_name
terraform init
terraform plan
terraform apply
```

Then point `kubectl` at the new cluster (`terraform output
configure_kubectl`) and apply the Phase 17 manifests:
`kubectl apply -k k8s/overlays/dev`.

## What this does and doesn't wire up yet

- **Redis**: genuinely consumed once you set `GATEWAY_REDIS_ADDR` (see
  `terraform output redis_address`) — `k8s/base/gateway-go-configmap.yaml`
  currently points it at the in-cluster Docker-Compose-style Redis
  Service instead; update that ConfigMap (or add a `dev`/`prod` overlay
  patch, the same pattern `k8s/overlays/dev` already uses elsewhere) to
  point at this Redis instance instead once you're running against real
  AWS infrastructure.
- **Postgres, S3, Secrets Manager**: provisioned, not yet consumed —
  `auth.UserRepository`/`ingestion.{Document,Job}Repository` are still
  the in-memory implementations, and `gateway-go`'s config is still read
  from plain env vars/`k8s` Secrets, not fetched from Secrets Manager at
  runtime. The IRSA roles this module creates
  (`gateway_go_irsa_role_arn`/`ml_service_irsa_role_arn` outputs) are
  what a future Postgres-backed repository or Secrets-Manager-reading
  config loader would assume via each pod's Kubernetes service account —
  the permission model is real and ready; nothing in the Go/Python code
  uses it yet. Wiring that up means adding a `ServiceAccount` (with the
  `eks.amazonaws.com/role-arn` annotation set to the relevant output) to
  `k8s/base/`, which doesn't exist there yet either.
