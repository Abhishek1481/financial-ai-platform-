variable "bucket_name" {
  description = "Globally-unique S3 bucket name for uploaded documents — the eventual replacement for gateway-go's LocalObjectStore (see gateway-go/README.md's design-decisions section)."
  type        = string
}

variable "tags" {
  type    = map(string)
  default = {}
}
