variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "azs" {
  type    = list(string)
  default = ["us-east-1a", "us-east-1b"]
}

# S3 bucket names are globally unique across all of AWS, not just this
# account — no reasonable default exists, so this has no `default` and
# must be set in terraform.tfvars (see that file's own comment).
variable "documents_bucket_name" {
  type = string
}
