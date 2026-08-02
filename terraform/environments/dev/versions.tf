terraform {
  required_version = ">= 1.6"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }

  # S3 backend, commented out rather than active by default: it needs the
  # bucket (and, ideally, a DynamoDB table for state locking) to already
  # exist, which is itself a bootstrapping step outside this
  # configuration's own responsibility — Terraform can't create the
  # backend it's about to store its own state in. Uncomment and fill in
  # once that bucket exists; until then, `terraform init` uses local
  # state, which is fine for evaluating this configuration but not for
  # any real, shared-with-a-team usage.
  #
  # backend "s3" {
  #   bucket         = "financial-ai-platform-terraform-state"
  #   key            = "dev/terraform.tfstate"
  #   region         = "us-east-1"
  #   dynamodb_table = "financial-ai-platform-terraform-locks"
  #   encrypt        = true
  # }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = local.tags
  }
}
