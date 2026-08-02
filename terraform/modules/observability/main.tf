# CloudWatch log groups both services' JSON structured logs (see
# gateway-go/internal/logging and ml-service/app/logging.py — both
# already emit one-JSON-object-per-line, exactly what CloudWatch Logs
# Insights expects) would ship to via a log-forwarding daemonset
# (Fluent Bit, or EKS's built-in Container Insights) — that daemonset
# itself is a Helm chart / k8s manifest concern, not provisioned here,
# same reasoning docker-compose.yml and k8s/base/ don't hand-roll
# Prometheus/Grafana as Terraform resources either.

resource "aws_cloudwatch_log_group" "gateway_go" {
  name              = "/financial-ai-platform/${var.name}/gateway-go"
  retention_in_days = var.retention_in_days
  tags              = var.tags
}

resource "aws_cloudwatch_log_group" "ml_service" {
  name              = "/financial-ai-platform/${var.name}/ml-service"
  retention_in_days = var.retention_in_days
  tags              = var.tags
}

# Counts ERROR-level lines in gateway-go's structured logs — a metric
# filter, not a separate agent, since the log lines are already JSON
# (`{"level":"ERROR",...}`) once they reach this log group.
resource "aws_cloudwatch_log_metric_filter" "gateway_go_errors" {
  name           = "${var.name}-gateway-go-error-count"
  log_group_name = aws_cloudwatch_log_group.gateway_go.name
  pattern        = "{ $.level = \"ERROR\" }"

  metric_transformation {
    name      = "GatewayGoErrorCount"
    namespace = "FinancialAiPlatform/${var.name}"
    value     = "1"
    default_value = "0"
  }
}

resource "aws_cloudwatch_metric_alarm" "gateway_go_error_rate" {
  alarm_name          = "${var.name}-gateway-go-high-error-rate"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods   = 1
  metric_name         = aws_cloudwatch_log_metric_filter.gateway_go_errors.metric_transformation[0].name
  namespace           = aws_cloudwatch_log_metric_filter.gateway_go_errors.metric_transformation[0].namespace
  period              = 300
  statistic           = "Sum"
  threshold           = 10
  treat_missing_data  = "notBreaching"
  alarm_description   = "More than 10 ERROR-level gateway-go log lines in a 5-minute window."
  tags                = var.tags
}
