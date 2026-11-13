# Database backups: every day the VM dumps the lobby's database and every
# tenant's, and uploads the dumps here (deploy/README.md, "Backups").
# Postgres's data is the only state on the VM that the repository and the
# deploy bucket can't rebuild.
#
# The VM's access key can only upload new dumps and publish the metric that
# says a backup succeeded. It can't read, list or delete a backup, so a
# compromised VM can't destroy them. Versioning keeps an overwritten dump, too.
# New buckets encrypt objects (SSE-S3) and disable ACLs on their own.

locals {
  backup_bucket           = "${var.name}-backups-${data.aws_caller_identity.current.account_id}"
  backup_prefix           = "postgres/"
  backup_metric_namespace = "Lobby/VM"
  backup_metric_name      = "DatabaseBackups"
  # Roles that may never download a dump: the GitHub roles of this
  # repository (github.tf and deploy/github_apply_role.py), whose
  # ReadOnlyAccess would otherwise let a workflow run read them, and any
  # other the settings name.
  backup_denied_roles = concat(
    local.github_deploy ? ["${var.name}-github-deploy", "${var.name}-github-apply"] : [],
    var.backup_denied_roles,
  )
}

resource "aws_s3_bucket" "backups" {
  bucket = local.backup_bucket

  lifecycle {
    # Terraform can't delete a bucket that has objects anyway; this stops the
    # plan before it tries.
    prevent_destroy = true
  }
}

resource "aws_s3_bucket_public_access_block" "backups" {
  bucket                  = aws_s3_bucket.backups.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_versioning" "backups" {
  bucket = aws_s3_bucket.backups.id

  versioning_configuration {
    status = "Enabled"
  }
}

# Each dump is kept for database_backup_retention_days. A dump that something
# overwrote or deleted is kept as a noncurrent version for as long again.
resource "aws_s3_bucket_lifecycle_configuration" "backups" {
  bucket = aws_s3_bucket.backups.id

  rule {
    id     = "expire-old-backups"
    status = "Enabled"

    filter {}

    expiration {
      days = var.database_backup_retention_days
    }

    noncurrent_version_expiration {
      noncurrent_days = var.database_backup_retention_days
    }

    abort_incomplete_multipart_upload {
      days_after_initiation = 1
    }
  }

  depends_on = [aws_s3_bucket_versioning.backups]
}

# Dumps hold player data. The bucket refuses plain HTTP, and keeps them from
# the GitHub roles. A condition on the caller's ARN, unlike a principal, may
# name a role that doesn't exist yet.
resource "aws_s3_bucket_policy" "backups" {
  bucket = aws_s3_bucket.backups.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat(
      [{
        Sid       = "RequireTls"
        Effect    = "Deny"
        Principal = "*"
        Action    = "s3:*"
        Resource  = [aws_s3_bucket.backups.arn, "${aws_s3_bucket.backups.arn}/*"]
        Condition = { Bool = { "aws:SecureTransport" = "false" } }
      }],
      length(local.backup_denied_roles) > 0 ? [{
        Sid       = "KeepDumpsFromGitHubRoles"
        Effect    = "Deny"
        Principal = "*"
        Action    = ["s3:GetObject", "s3:GetObjectVersion"]
        Resource  = "${aws_s3_bucket.backups.arn}/*"
        Condition = {
          ArnEquals = {
            "aws:PrincipalArn" = [for role in local.backup_denied_roles : "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${role}"]
          }
        }
      }] : [],
    )
  })

  depends_on = [aws_s3_bucket_public_access_block.backups]
}

# Lightsail instances can't take an IAM role, so the VM gets a user's access
# key. The VM deploy installs it, readable only by root
# (deploy/ansible/roles/app/tasks/backups.yml).
resource "aws_iam_user" "backups" {
  name = "${var.name}-database-backups"
}

resource "aws_iam_user_policy" "backups" {
  name = "upload-backups"
  user = aws_iam_user.backups.name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "UploadDumps"
        Effect   = "Allow"
        Action   = "s3:PutObject"
        Resource = "${aws_s3_bucket.backups.arn}/${local.backup_prefix}*"
      },
      {
        Sid      = "CountBackups"
        Effect   = "Allow"
        Action   = "cloudwatch:PutMetricData"
        Resource = "*"
        Condition = {
          StringEquals = { "cloudwatch:namespace" = local.backup_metric_namespace }
        }
      },
    ]
  })
}

# The secret is in Terraform's state, like the Lambda's settings, and
# scripts/aws.sh copies it to deploy/local/secrets/database_backups.json after
# every apply. To rotate it, replace it with
# `scripts/aws.sh apply -replace=aws_iam_access_key.backups`, then deploy the VM.
resource "aws_iam_access_key" "backups" {
  user = aws_iam_user.backups.name
}

# Every successful backup adds 1 to the metric, which has no dimensions: 1 more
# of the 10 free custom metrics. The alarm, 1 more of the 10 free alarms, emails
# when no backup succeeded for 26 hours in a row: the daily run failed, the VM
# is down, or its key stopped working. Missing data counts as no backup, so it
# also emails right after the first apply, until the first VM deploy backs up.
# It covers more than a day, so CloudWatch evaluates it once an hour.
resource "aws_cloudwatch_metric_alarm" "database_backups" {
  alarm_name          = "${var.name}-database-backups"
  alarm_description   = "No database backup reached S3 for 26 hours. On the VM, read `journalctl -u mpl-backup` (deploy/README.md, Backups)."
  namespace           = local.backup_metric_namespace
  metric_name         = local.backup_metric_name
  statistic           = "Sum"
  period              = 3600
  evaluation_periods  = 26
  datapoints_to_alarm = 26
  threshold           = 1
  comparison_operator = "LessThanThreshold"
  treat_missing_data  = "breaching"
  alarm_actions       = [aws_sns_topic.alerts.arn]
}
