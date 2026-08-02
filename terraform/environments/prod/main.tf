# prod environment root module — same modules as ../dev/main.tf, sized
# for production: NAT gateway per AZ (no single point of failure for
# private-subnet egress), multi-AZ Postgres (automatic failover), larger/
# more node instances, and 90-day log retention instead of dev's 14.

locals {
  name        = "financial-ai-platform-prod"
  db_username = "financial_ai_platform"
  tags = {
    Project     = "financial-ai-platform"
    Environment = "prod"
    ManagedBy   = "terraform"
  }
}

resource "random_password" "db_password" {
  length  = 32
  special = false
}

module "networking" {
  source = "../../modules/networking"

  name               = local.name
  azs                = var.azs
  single_nat_gateway = false # prod: one NAT per AZ — see the networking module's variable doc comment
  tags               = local.tags
}

module "storage" {
  source = "../../modules/storage"

  bucket_name = var.documents_bucket_name
  tags        = local.tags
}

module "database" {
  source = "../../modules/database"

  name                     = local.name
  vpc_id                   = module.networking.vpc_id
  vpc_cidr                 = module.networking.vpc_cidr
  private_subnet_ids       = module.networking.private_subnet_ids
  postgres_instance_class  = "db.t4g.small"
  postgres_multi_az        = true
  redis_node_type          = "cache.t4g.small"
  db_username              = local.db_username
  db_password              = random_password.db_password.result
  tags                     = local.tags
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
  retention_in_days = 90
  tags              = local.tags
}

module "eks" {
  source = "../../modules/eks"

  name                = local.name
  vpc_id              = module.networking.vpc_id
  private_subnet_ids  = module.networking.private_subnet_ids
  public_subnet_ids   = module.networking.public_subnet_ids
  node_instance_types = ["t3.large"]
  node_desired_size   = 3
  node_min_size       = 2
  node_max_size       = 8
  s3_bucket_arn       = module.storage.bucket_arn
  secret_arns = [
    module.secrets.jwt_secret_arn,
    module.secrets.admin_password_arn,
    module.secrets.llm_api_key_secret_arn,
    module.secrets.db_credentials_arn,
  ]
  tags = local.tags
}
