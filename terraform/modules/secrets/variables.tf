variable "name" {
  type = string
}

variable "db_username" {
  type = string
}

variable "db_password" {
  description = "Same value the database module's aws_db_instance is created with — stored here too so the EKS workloads have a Secrets Manager entry to read it from, rather than the two modules disagreeing about what the password is."
  type        = string
  sensitive   = true
}

variable "tags" {
  type    = map(string)
  default = {}
}
