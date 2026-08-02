# dev environment root module — composes every reusable module in
# ../../modules with dev-appropriate sizing (single NAT gateway,
# single-AZ Postgres, small instance types). See ../prod/main.tf for the
# same composition at production sizing; the modules themselves don't
# differ between the two, only what each root module passes in.

locals {
  name        = "financial-ai-platform-dev"
  db_username = "financial_ai_platform"
  tags = {
    Project     = "financial-ai-platform"
    Environment = "dev"
    ManagedBy   = "terraform"
  }
}

resource "random_password" "db_password" {
  length  = 32
  special = false # RDS's master password field rejects some special characters; alphanumeric avoids that entirely rather than trying to enumerate which ones
}

module "networking" {
  source = "../../modules/networking"

  name               = local.name
  azs                = var.azs
  single_nat_gateway = true # dev: cost over redundancy — see the networking module's variable doc comment
  tags               = local.tags
}

module "storage" {
  source = "../../modules/storage"

  bucket_name = var.documents_bucket_name
  tags        = local.tags
}

module "database" {
  source = "../../modules/database"

  name               = local.name
  vpc_id             = module.networking.vpc_id
  vpc_cidr           = module.networking.vpc_cidr
  private_subnet_ids = module.networking.private_subnet_ids
  postgres_multi_az  = false
  db_username        = local.db_username
  db_password        = random_password.db_password.result
  tags               = local.tags
}

module "secrets" {
  source = "../../modules/secrets"

  name        = local.name
  db_username = local.db_username
  db_password = random_password.db_password.result
  tags        = local.tags
}

module "observability" {
  source = "../../modules/observability"

  name              = local.name
  retention_in_days = 14 # dev: short retention, matches the cost-over-durability choices elsewhere in this root module
  tags              = local.tags
}

module "eks" {
  source = "../../modules/eks"

  name                 = local.name
  vpc_id               = module.networking.vpc_id
  private_subnet_ids   = module.networking.private_subnet_ids
  public_subnet_ids    = module.networking.public_subnet_ids
  node_instance_types  = ["t3.medium"]
  node_desired_size    = 2
  node_min_size        = 1
  node_max_size        = 4
  s3_bucket_arn        = module.storage.bucket_arn
  secret_arns = [
    module.secrets.jwt_secret_arn,
    module.secrets.admin_password_arn,
    module.secrets.llm_api_key_secret_arn,
    module.secrets.db_credentials_arn,
  ]
  tags = local.tags
}
