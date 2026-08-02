output "jwt_secret_arn" {
  value = aws_secretsmanager_secret.jwt_secret.arn
}

output "admin_password_arn" {
  value = aws_secretsmanager_secret.admin_password.arn
}

output "llm_api_key_secret_arn" {
  value = aws_secretsmanager_secret.llm_api_key.arn
}

output "db_credentials_arn" {
  value = aws_secretsmanager_secret.db_credentials.arn
}
