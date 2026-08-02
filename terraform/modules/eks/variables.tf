variable "name" {
  type = string
}

variable "kubernetes_version" {
  type    = string
  default = "1.31"
}

variable "vpc_id" {
  type = string
}

variable "private_subnet_ids" {
  description = "Node group and the cluster's private endpoint both live here — see the networking module."
  type        = list(string)
}

variable "public_subnet_ids" {
  description = "Only used for the cluster's public API endpoint's ENIs, never for worker nodes — nodes are private-subnet-only (node_subnet_ids below), so this list existing doesn't put any compute in a public subnet."
  type        = list(string)
}

variable "node_instance_types" {
  type    = list(string)
  default = ["t3.medium"]
}

variable "node_desired_size" {
  type    = number
  default = 2
}

variable "node_min_size" {
  type    = number
  default = 1
}

variable "node_max_size" {
  type    = number
  default = 4
}

variable "s3_bucket_arn" {
  description = "Granted to the gateway-go IRSA role for read/write access to uploaded documents (see the storage module)."
  type        = string
}

variable "secret_arns" {
  description = "Secrets Manager ARNs the gateway-go/ml-service IRSA roles can read (see the secrets module) — JWT secret, admin password, LLM API key, DB credentials."
  type        = list(string)
}

variable "tags" {
  type    = map(string)
  default = {}
}
