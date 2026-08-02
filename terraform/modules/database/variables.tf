variable "name" {
  type = string
}

variable "vpc_id" {
  type = string
}

variable "vpc_cidr" {
  description = "Ingress on the DB/Redis ports is scoped to this CIDR rather than a specific security group, so this module doesn't need a circular dependency on the EKS module's node security group — private subnets already aren't internet-reachable (see the networking module), so VPC-CIDR-scoped ingress is a reasonable boundary, not the only one."
  type        = string
}

variable "private_subnet_ids" {
  type = list(string)
}

variable "postgres_instance_class" {
  type    = string
  default = "db.t4g.micro"
}

variable "postgres_allocated_storage_gb" {
  type    = number
  default = 20
}

variable "postgres_multi_az" {
  description = "true for prod (a standby in a second AZ, automatic failover) — costs roughly double a single-AZ instance, which is why dev defaults to false."
  type        = bool
  default     = false
}

variable "redis_node_type" {
  type    = string
  default = "cache.t4g.micro"
}

variable "db_name" {
  type    = string
  default = "financial_ai_platform"
}

variable "db_username" {
  type    = string
  default = "financial_ai_platform"
}

variable "db_password" {
  description = "Provisioned into Secrets Manager by the secrets module, not read from a .tfvars file — see environments/dev/main.tf for how these two modules wire together."
  type        = string
  sensitive   = true
}

variable "tags" {
  type    = map(string)
  default = {}
}
