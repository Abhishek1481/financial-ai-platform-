# modules

Reusable Terraform modules, composed by `../environments/{dev,prod}`:

- `networking/` — VPC, public/private subnets across the given AZs,
  internet gateway, NAT gateway(s)
- `eks/` — EKS cluster, managed node group, IAM roles for both, plus the
  OIDC provider + IRSA roles (`gateway-go`, `ml-service`) that let a pod
  assume a scoped IAM role via its Kubernetes service account
- `storage/` — S3 bucket for uploaded documents (versioned, encrypted,
  fully private)
- `database/` — RDS Postgres + a single-node ElastiCache Redis
- `secrets/` — Secrets Manager entries for the JWT signing secret, admin
  password, LLM API key (empty placeholder), and DB credentials
- `observability/` — CloudWatch log groups per service, plus an example
  metric filter + alarm on gateway-go's ERROR-level log lines

See `/terraform/README.md` for the composition, the verification caveat,
and what's provisioned-but-not-yet-consumed by the application code.
