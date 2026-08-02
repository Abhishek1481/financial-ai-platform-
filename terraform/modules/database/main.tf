# RDS Postgres and ElastiCache Redis — both provisioned ahead of the code
# that would use them, same as the storage module's S3 bucket. Postgres
# is what auth.UserRepository/ingestion.{Document,Job}Repository would
# migrate to (still in-memory today — see k8s/README.md); Redis is what
# gateway-go actually already talks to once GATEWAY_REDIS_ADDR is set
# (internal/cache/redis_cache.go, internal/conversation/redis_store.go).

resource "aws_db_subnet_group" "this" {
  name       = "${var.name}-postgres"
  subnet_ids = var.private_subnet_ids
  tags       = var.tags
}

resource "aws_elasticache_subnet_group" "this" {
  name       = "${var.name}-redis"
  subnet_ids = var.private_subnet_ids
}

resource "aws_security_group" "postgres" {
  name_prefix = "${var.name}-postgres-"
  vpc_id      = var.vpc_id
  tags        = var.tags

  ingress {
    description = "Postgres from within the VPC"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_security_group" "redis" {
  name_prefix = "${var.name}-redis-"
  vpc_id      = var.vpc_id
  tags        = var.tags

  ingress {
    description = "Redis from within the VPC"
    from_port   = 6379
    to_port     = 6379
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_db_instance" "postgres" {
  identifier     = "${var.name}-postgres"
  engine         = "postgres"
  engine_version = "16"

  instance_class        = var.postgres_instance_class
  allocated_storage     = var.postgres_allocated_storage_gb
  storage_type          = "gp3"
  storage_encrypted     = true
  multi_az              = var.postgres_multi_az
  db_subnet_group_name  = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.postgres.id]

  db_name  = var.db_name
  username = var.db_username
  password = var.db_password

  backup_retention_period = var.postgres_multi_az ? 7 : 1
  skip_final_snapshot     = !var.postgres_multi_az # dev: skip (disposable); prod (multi_az=true): keep a final snapshot on destroy
  deletion_protection     = var.postgres_multi_az

  tags = var.tags
}

# A single-node cluster, not an aws_elasticache_replication_group — the
# in-memory MemoryCache/MemoryStore this replaces already accept "a
# restart loses the data" (see gateway-go/internal/cache's doc comment on
# why that's fine for a cache), so a single Redis node with no automatic
# failover is a defensible starting point, not an oversight. Multi-node
# replication is the natural upgrade once conversation memory durability
# actually matters enough to justify it — a variable-gated choice this
# module doesn't make on its own.
resource "aws_elasticache_cluster" "redis" {
  cluster_id           = "${var.name}-redis"
  engine               = "redis"
  engine_version       = "7.1"
  node_type            = var.redis_node_type
  num_cache_nodes      = 1
  port                 = 6379
  subnet_group_name    = aws_elasticache_subnet_group.this.name
  security_group_ids   = [aws_security_group.redis.id]

  tags = var.tags
}
