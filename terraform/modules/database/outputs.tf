output "postgres_endpoint" {
  value = aws_db_instance.postgres.endpoint
}

output "postgres_database_url" {
  description = "Full connection string, in the same shape a future GATEWAY_DATABASE_URL setting would expect."
  value       = "postgres://${var.db_username}:${var.db_password}@${aws_db_instance.postgres.endpoint}/${var.db_name}"
  sensitive   = true
}

output "redis_address" {
  description = "In the same host:port shape GATEWAY_REDIS_ADDR already expects (see internal/config/config.go) — plug this straight in."
  value       = "${aws_elasticache_cluster.redis.cache_nodes[0].address}:${aws_elasticache_cluster.redis.cache_nodes[0].port}"
}
