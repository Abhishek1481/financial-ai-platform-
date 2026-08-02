# Secrets Manager entries for exactly the values k8s/base/gateway-go-secret.yaml
# and ml-service-secret.yaml currently ship as insecure plaintext
# placeholders — the intended replacement for those once a cluster
# actually pulls from Secrets Manager (via the External Secrets Operator,
# or AWS Secrets and Configuration Provider for the Secrets Store CSI
# Driver), not a parallel, disconnected secret store.
#
# random_password generates real random values here rather than
# hard-coding anything — the JWT signing secret and admin password never
# need to be human-readable or memorable, unlike a database password an
# operator might occasionally type by hand (db_password is still an
# input, not generated, so it can also be the exact value the database
# module's aws_db_instance was created with).

resource "random_password" "jwt_secret" {
  length  = 64
  special = true
}

resource "aws_secretsmanager_secret" "jwt_secret" {
  name = "${var.name}/gateway-jwt-secret"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "jwt_secret" {
  secret_id     = aws_secretsmanager_secret.jwt_secret.id
  secret_string = random_password.jwt_secret.result
}

resource "random_password" "admin_password" {
  length  = 32
  special = true
}

resource "aws_secretsmanager_secret" "admin_password" {
  name = "${var.name}/gateway-admin-password"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "admin_password" {
  secret_id     = aws_secretsmanager_secret.admin_password.id
  secret_string = random_password.admin_password.result
}

# Left empty deliberately — there's no LLM API key available to generate
# or provision in this environment (see ml-service/app/rag/llm_client.py
# for why "fake" is this whole project's honest default provider). The
# secret shell exists so populating it later is "paste a value into an
# existing entry," not "create new infrastructure."
resource "aws_secretsmanager_secret" "llm_api_key" {
  name = "${var.name}/ml-service-llm-api-key"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "llm_api_key" {
  secret_id     = aws_secretsmanager_secret.llm_api_key.id
  secret_string = ""

  lifecycle {
    # Once someone has actually populated this via the console/CLI, a
    # `terraform apply` should never stomp their value back to empty.
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "db_credentials" {
  name = "${var.name}/postgres-credentials"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "db_credentials" {
  secret_id = aws_secretsmanager_secret.db_credentials.id
  secret_string = jsonencode({
    username = var.db_username
    password = var.db_password
  })
}
