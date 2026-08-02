variable "name" {
  type = string
}

variable "retention_in_days" {
  description = "CloudWatch Logs retention — 14 days is a reasonable default for a portfolio/dev deployment; prod environments typically want longer (30-90 days) for incident investigation."
  type        = number
  default     = 14
}

variable "tags" {
  type    = map(string)
  default = {}
}
