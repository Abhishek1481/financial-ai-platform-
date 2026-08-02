# environments

Per-environment Terraform root modules composing `../modules/` —
`dev/` (single NAT gateway, single-AZ Postgres, small instances) and
`prod/` (one NAT gateway per AZ, multi-AZ Postgres, larger/more
instances, longer log retention). No `staging/` yet — add one the same
way once there's an actual staging AWS account/VPC to target. See
`/terraform/README.md` for how to apply either one.
