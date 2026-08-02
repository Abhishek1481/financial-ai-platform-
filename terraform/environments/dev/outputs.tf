output "cluster_name" {
  value = module.eks.cluster_name
}

output "configure_kubectl" {
  value = module.eks.configure_kubectl
}

output "documents_bucket_name" {
  value = module.storage.bucket_name
}

output "redis_address" {
  description = "Set as GATEWAY_REDIS_ADDR (see k8s/base/gateway-go-configmap.yaml, which currently points it at the in-cluster Redis Service instead — update it to this once you're running against real AWS infrastructure rather than the Docker Compose stack)."
  value       = module.database.redis_address
}

output "postgres_endpoint" {
  value = module.database.postgres_endpoint
}

output "gateway_go_irsa_role_arn" {
  value = module.eks.gateway_go_irsa_role_arn
}

output "ml_service_irsa_role_arn" {
  value = module.eks.ml_service_irsa_role_arn
}
