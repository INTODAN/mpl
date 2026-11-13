# scripts/aws.sh output <name> prints one of these. The VM deploy takes the
# static IP and, at the cutover, the Function URL. The DNS records take the
# static IP, the certificate's validation record and the distribution's
# domain (deploy/README.md, "DNS records").

output "static_ip" {
  description = "The VM's static IP. The vm.<domain> A record points at it."
  value       = aws_lightsail_static_ip.vm.ip_address
}

output "instance_ips" {
  description = "Each instance's public IP. The instance the static IP is attached to shows the static IP."
  value       = { for name, instance in aws_lightsail_instance.vm : name => instance.public_ip_address }
}

output "function_url" {
  description = "The lobby's own address, which CloudFront calls, and Centrifugo's centrifugo_proxy_url in deploy/local/vm.yml once the cutover switches it."
  value       = aws_lambda_function_url.lobby.function_url
}

output "function_name" {
  description = "The lobby Lambda's name."
  value       = aws_lambda_function.lobby.function_name
}

output "lobby_url" {
  description = "Where players and games reach the lobby, and lobby_url in deploy/local/vm.yml."
  value       = "https://${var.lobby_hostname}"
}

output "certificate_validation_record" {
  description = "The CNAME that lets ACM issue the lobby's certificate and renew it. Keep it in the domain's DNS."
  value       = join("\n", [for option in aws_acm_certificate.lobby.domain_validation_options : "${option.resource_record_name} CNAME ${option.resource_record_value}"])
}

output "distribution_domain" {
  description = "The CloudFront distribution's own name, which the lobby_hostname CNAME points at."
  value       = aws_cloudfront_distribution.lobby.domain_name
}

output "lobby_dns_record" {
  description = "The CNAME to add for the lobby's name once the distribution exists."
  value       = "${var.lobby_hostname}. CNAME ${aws_cloudfront_distribution.lobby.domain_name}."
}

output "distribution_id" {
  description = "The CloudFront distribution's ID."
  value       = aws_cloudfront_distribution.lobby.id
}

output "database_backup_bucket" {
  description = "The S3 bucket that holds the daily database backups, under postgres/."
  value       = aws_s3_bucket.backups.bucket
}

# scripts/aws.sh writes this to deploy/local/secrets/database_backups.json
# after every apply, and the VM deploy installs it on the VM.
output "database_backups" {
  description = "What the VM's daily backup needs, its upload-only access key included."
  sensitive   = true
  value = {
    bucket            = aws_s3_bucket.backups.bucket
    region            = var.region
    prefix            = local.backup_prefix
    metric_namespace  = local.backup_metric_namespace
    metric_name       = local.backup_metric_name
    access_key_id     = aws_iam_access_key.backups.id
    secret_access_key = aws_iam_access_key.backups.secret
  }
}

output "github_deploy_role_arn" {
  description = "The role the Deploy workflow assumes: the repository's AWS_DEPLOY_ROLE_ARN variable. Null without github_repository."
  value       = try(aws_iam_role.github_deploy[0].arn, null)
}
