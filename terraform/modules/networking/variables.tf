variable "name" {
  description = "Name prefix for every resource this module creates (e.g. \"financial-ai-platform-dev\")."
  type        = string
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC."
  type        = string
  default     = "10.0.0.0/16"
}

variable "azs" {
  description = "Availability zones to spread subnets across. Two is the practical minimum for anything with a managed NAT/RDS/EKS control plane, which all expect multi-AZ placement."
  type        = list(string)
}

variable "single_nat_gateway" {
  description = "true: one shared NAT gateway (cheaper, a single point of failure for private-subnet egress — fine for dev). false: one NAT gateway per AZ (no single point of failure — what prod should use)."
  type        = bool
  default     = true
}

variable "tags" {
  description = "Tags applied to every resource this module creates."
  type        = map(string)
  default     = {}
}
