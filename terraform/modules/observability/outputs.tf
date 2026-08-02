output "gateway_go_log_group_name" {
  value = aws_cloudwatch_log_group.gateway_go.name
}

output "ml_service_log_group_name" {
  value = aws_cloudwatch_log_group.ml_service.name
}
