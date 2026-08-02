# Provisioned ahead of the code that would use it — LocalObjectStore
# (gateway-go/internal/storage) and file:// URIs (see
# k8s/README.md's "gateway-go and ml-service share a ReadWriteMany PVC")
# are still what actually runs. This bucket, and an s3:// ObjectStore
# implementation satisfying the same interface, is the follow-up that
# removes the RWX-PVC constraint entirely — a URI crossing a network
# boundary instead of a filesystem boundary needs no shared volume.

resource "aws_s3_bucket" "documents" {
  bucket = var.bucket_name
  tags   = var.tags
}

resource "aws_s3_bucket_versioning" "documents" {
  bucket = aws_s3_bucket.documents.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "documents" {
  bucket = aws_s3_bucket.documents.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "aws:kms"
    }
    bucket_key_enabled = true
  }
}

# Uploaded documents (SEC filings, financial reports) are never meant to
# be served directly to a browser — only ml-service and gateway-go ever
# read them, both server-side over the AWS SDK, never via a public URL —
# so nothing about this bucket should ever be publicly reachable.
resource "aws_s3_bucket_public_access_block" "documents" {
  bucket = aws_s3_bucket.documents.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_lifecycle_configuration" "documents" {
  bucket = aws_s3_bucket.documents.id

  rule {
    id     = "expire-noncurrent-versions"
    status = "Enabled"

    # Versioning (above) protects against accidental overwrite/delete,
    # not indefinite storage of every past version — content-hash dedup
    # (ingestion.Service.Upload) already means the same bytes are never
    # uploaded twice under a new key, so noncurrent versions here are
    # genuinely superseded content, not just duplicates.
    noncurrent_version_expiration {
      noncurrent_days = 90
    }
  }
}
