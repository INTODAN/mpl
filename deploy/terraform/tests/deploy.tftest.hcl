# Offline checks of the configuration's own logic. Both providers are mocked,
# so these plans never call AWS and need no credentials or built binary:
#
#   terraform -chdir=deploy/terraform init -backend=false
#   terraform -chdir=deploy/terraform test

mock_provider "aws" {}
mock_provider "aws" {
  alias = "vm"
}
mock_provider "archive" {}

variables {
  region                = "eu-central-1"
  lobby_hostname        = "lobby.example.org"
  lambda_settings_file  = "tests/fixtures/lobby.env"
  lambda_bootstrap_file = "tests/fixtures/bootstrap"
  vm_instances = {
    "game-1" = {}
  }
  static_ip_instance = "game-1"
  adopt_vm           = false
  alert_email        = "alerts@example.org"
}

# The lobby Lambda ------------------------------------------------------------

run "runs_the_lobby_as_decided" {
  command = plan

  assert {
    condition     = aws_lambda_function.lobby.function_name == "mpl" && aws_lambda_function.lobby.runtime == "provided.al2023" && aws_lambda_function.lobby.architectures == tolist(["arm64"]) && aws_lambda_function.lobby.handler == "bootstrap"
    error_message = "The function must run the arm64 bootstrap on provided.al2023."
  }

  assert {
    condition     = aws_lambda_function.lobby.memory_size == 512 && aws_lambda_function.lobby.timeout == 30 && aws_lambda_function.lobby.reserved_concurrent_executions == 5
    error_message = "The function must have 512 MB, 30 seconds and a reserved concurrency of 5."
  }

  assert {
    condition     = aws_lambda_function_url.lobby.authorization_type == "NONE" && aws_lambda_function_url.lobby.invoke_mode == "RESPONSE_STREAM"
    error_message = "The Function URL must be public and streamed."
  }

  assert {
    condition = (
      aws_iam_role_policy_attachment.lambda_logs.policy_arn == "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole" &&
      aws_iam_role_policy_attachment.lambda_traces.policy_arn == "arn:aws:iam::aws:policy/AWSXRayDaemonWriteAccess" &&
      aws_iam_role_policy_attachment.lambda_logs.role == "mpl-lambda" && aws_iam_role_policy_attachment.lambda_traces.role == "mpl-lambda"
    )
    error_message = "The role must have basic execution and X-Ray writes only."
  }

  assert {
    condition = (
      aws_lambda_function.lobby.logging_config[0].log_format == "JSON" &&
      aws_lambda_function.lobby.logging_config[0].application_log_level == "WARN" &&
      aws_lambda_function.lobby.logging_config[0].system_log_level == "WARN" &&
      aws_lambda_function.lobby.logging_config[0].log_group == "/aws/lambda/mpl" &&
      aws_cloudwatch_log_group.lobby.retention_in_days == 7 &&
      aws_lambda_function.lobby.tracing_config[0].mode == "PassThrough"
    )
    error_message = "Only warnings and errors may reach a log group that keeps them for 7 days, without active tracing."
  }
}

run "passes_every_lambda_setting" {
  command = plan

  assert {
    condition = tomap({ for key, value in nonsensitive(aws_lambda_function.lobby.environment[0].variables) : key => value }) == tomap({
      LOBBY_DATABASE_URL                  = "postgres://lobby:not-a-secret@vm.example.org:5432/lobby?sslmode=require"
      LOBBY_PUBLIC_URL                    = "https://lobby.example.org"
      LOBBY_CENTRIFUGO_PUBLIC_URL         = "wss://vm.example.org/connection/websocket"
      LOBBY_CENTRIFUGO_API_URL            = "https://vm.example.org/api"
      LOBBY_CENTRIFUGO_API_KEY            = "not-a-secret"
      LOBBY_PROXY_SECRET                  = "not-a-secret"
      LOBBY_SETTING_TERRAFORM_DOESNT_KNOW = "a=b"
      LOBBY_DATABASE_MAX_OPEN_CONNECTIONS = "2"
    })
    error_message = "The function must get every lobby.env setting, the last value of a repeated key, and the pool size."
  }
}

run "takes_extra_settings" {
  command = plan

  variables {
    extra_lambda_environment = {
      LOBBY_RETENTION_IDLE_PERIOD = "2h"
    }
  }

  assert {
    condition     = nonsensitive(aws_lambda_function.lobby.environment[0].variables["LOBBY_RETENTION_IDLE_PERIOD"]) == "2h"
    error_message = "Extra settings must reach the function."
  }
}

run "refuses_missing_settings" {
  command = plan

  variables {
    lambda_settings_file = "tests/fixtures/missing.env"
  }

  expect_failures = [aws_lambda_function.lobby]
}

run "allows_missing_settings_on_the_first_apply" {
  command = plan

  variables {
    lambda_settings_file          = "tests/fixtures/missing.env"
    allow_missing_lambda_settings = true
  }

  assert {
    condition     = nonsensitive(aws_lambda_function.lobby.environment[0].variables) == tomap({ LOBBY_DATABASE_MAX_OPEN_CONNECTIONS = "2" })
    error_message = "Without settings, the function gets only what Terraform sets."
  }
}

run "refuses_malformed_settings" {
  command = plan

  variables {
    lambda_settings_file = "tests/fixtures/malformed.env"
  }

  expect_failures = [aws_lambda_function.lobby]
}

run "refuses_a_pool_size_in_the_settings" {
  command = plan

  variables {
    lambda_settings_file = "tests/fixtures/pool-setting.env"
  }

  expect_failures = [aws_lambda_function.lobby]
}

run "refuses_too_many_database_connections" {
  command = plan

  variables {
    # 5 × 15 + 20 for the tenants = 95 leaves 5 of 100 free, fewer than the
    # 10 spare connections.
    database_max_open_connections = 15
  }

  expect_failures = [aws_lambda_function.lobby]
}

run "allows_the_largest_safe_pool" {
  command = plan

  variables {
    # 5 × 14 + 20 = 90 leaves exactly the 10 spare connections.
    database_max_open_connections = 14
  }

  assert {
    condition     = nonsensitive(aws_lambda_function.lobby.environment[0].variables["LOBBY_DATABASE_MAX_OPEN_CONNECTIONS"]) == "14"
    error_message = "5 × 14 + 20 = 90 leaves exactly the 10 spare connections."
  }
}

run "counts_the_tenants_connections" {
  command = plan

  variables {
    tenant_database_connections = 81
  }

  expect_failures = [aws_lambda_function.lobby]
}

# The lobby's name ------------------------------------------------------------

run "serves_the_lobby_through_cloudfront" {
  command = plan

  # AWS assigns these on creation. Known during the plan, the distribution's
  # origin and certificate can be checked.
  override_resource {
    target          = aws_lambda_function_url.lobby
    override_during = plan
    values = {
      function_url = "https://abcdefghijklmnopqrstuvwxyz012345.lambda-url.eu-central-1.on.aws/"
    }
  }

  override_resource {
    target          = aws_acm_certificate.lobby
    override_during = plan
    values = {
      arn = "arn:aws:acm:us-east-1:123456789012:certificate/00000000-0000-4000-8000-000000000001"
    }
  }

  override_resource {
    target          = aws_cloudfront_cache_policy.origin_cache_control
    override_during = plan
    values = {
      id = "11111111-2222-3333-4444-555555555555"
    }
  }

  assert {
    condition     = aws_acm_certificate.lobby.region == "us-east-1" && aws_acm_certificate.lobby.domain_name == "lobby.example.org" && aws_acm_certificate.lobby.validation_method == "DNS" && aws_acm_certificate_validation.lobby.region == "us-east-1"
    error_message = "The certificate must be for lobby_hostname, validated by DNS, in us-east-1 where CloudFront takes it from."
  }

  assert {
    condition = (
      aws_cloudfront_distribution.lobby.aliases == toset(["lobby.example.org"]) &&
      aws_cloudfront_distribution.lobby.viewer_certificate[0].acm_certificate_arn == "arn:aws:acm:us-east-1:123456789012:certificate/00000000-0000-4000-8000-000000000001" &&
      aws_cloudfront_distribution.lobby.viewer_certificate[0].ssl_support_method == "sni-only"
    )
    error_message = "The distribution must serve lobby_hostname with the validated certificate."
  }

  assert {
    condition = (
      one(aws_cloudfront_distribution.lobby.origin).origin_id == "lobby" &&
      one(aws_cloudfront_distribution.lobby.origin).domain_name == "abcdefghijklmnopqrstuvwxyz012345.lambda-url.eu-central-1.on.aws" &&
      one(aws_cloudfront_distribution.lobby.origin).custom_origin_config[0].origin_protocol_policy == "https-only"
    )
    error_message = "The distribution must call the Function URL's host over HTTPS."
  }

  assert {
    condition = (
      toset(aws_cloudfront_distribution.lobby.default_cache_behavior[0].allowed_methods) == toset(["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]) &&
      aws_cloudfront_distribution.lobby.default_cache_behavior[0].viewer_protocol_policy == "redirect-to-https" &&
      data.aws_cloudfront_cache_policy.caching_disabled.name == "Managed-CachingDisabled" &&
      aws_cloudfront_distribution.lobby.default_cache_behavior[0].cache_policy_id == data.aws_cloudfront_cache_policy.caching_disabled.id &&
      data.aws_cloudfront_origin_request_policy.all_viewer_except_host_header.name == "Managed-AllViewerExceptHostHeader" &&
      aws_cloudfront_distribution.lobby.default_cache_behavior[0].origin_request_policy_id == data.aws_cloudfront_origin_request_policy.all_viewer_except_host_header.id
    )
    error_message = "Every other request must reach the Lambda uncached over HTTPS, with every method and every viewer header but Host."
  }

  assert {
    condition = (
      [for behavior in aws_cloudfront_distribution.lobby.ordered_cache_behavior : behavior.path_pattern] == ["/lobby/ui/*"] &&
      aws_cloudfront_distribution.lobby.ordered_cache_behavior[0].cache_policy_id == "11111111-2222-3333-4444-555555555555" &&
      toset(aws_cloudfront_distribution.lobby.ordered_cache_behavior[0].allowed_methods) == toset(["GET", "HEAD", "OPTIONS"]) &&
      aws_cloudfront_distribution.lobby.ordered_cache_behavior[0].compress
    )
    error_message = "The lobby page's files, and only they, must follow the origin's Cache-Control."
  }

  assert {
    condition = (
      aws_cloudfront_cache_policy.origin_cache_control.parameters_in_cache_key_and_forwarded_to_origin[0].headers_config[0].header_behavior == "none" &&
      aws_cloudfront_cache_policy.origin_cache_control.parameters_in_cache_key_and_forwarded_to_origin[0].cookies_config[0].cookie_behavior == "none" &&
      aws_cloudfront_cache_policy.origin_cache_control.default_ttl == 0
    )
    error_message = "The cache policy must key on the path alone, and keep nothing that has no Cache-Control."
  }

  assert {
    condition     = output.lobby_url == "https://lobby.example.org"
    error_message = "The lobby's URL is lobby_hostname over HTTPS."
  }
}

# The VM ----------------------------------------------------------------------

run "creates_a_new_vm" {
  command = plan

  assert {
    condition     = toset([for port in aws_lightsail_instance_public_ports.vm["game-1"].port_info : port.from_port]) == toset([22, 443, 5432])
    error_message = "The firewall must open 22, 443 and Postgres, and nothing else."
  }

  assert {
    condition     = one([for add_on in aws_lightsail_instance.vm["game-1"].add_on : add_on if add_on.type == "AutoSnapshot" && add_on.status == "Disabled"]) != null
    error_message = "The instance's automatic snapshots must be declared off: the databases are backed up to S3 instead."
  }

  assert {
    condition     = aws_lightsail_instance.vm["game-1"].ip_address_type == "ipv4" && aws_lightsail_instance.vm["game-1"].availability_zone == "eu-central-1a" && aws_lightsail_instance.vm["game-1"].blueprint_id == "ubuntu_24_04" && aws_lightsail_instance.vm["game-1"].bundle_id == "micro_3_0"
    error_message = "A new instance runs Ubuntu 24.04 on the 1 GB plan, IPv4 only, in the region's a zone."
  }

  assert {
    condition     = aws_lightsail_static_ip.vm.name == "mpl-vm" && aws_lightsail_static_ip_attachment.vm.instance_name == "game-1"
    error_message = "The static IP must be named after the deployment and attached to static_ip_instance."
  }
}

run "takes_over_a_running_vm" {
  command = plan

  variables {
    adopt_vm           = true
    manage_vm_firewall = false
    static_ip_name     = "game-vm"
    vm_tags = {
      Project   = "game"
      ManagedBy = "terraform"
    }
  }

  # A mocked provider can't import, so these stand for what Lightsail
  # returns. deploy/test_check_deploy_plan.py checks what the checker makes
  # of the real plan's imports.
  override_resource {
    target = aws_lightsail_instance.vm
    values = {
      name              = "game-1"
      availability_zone = "eu-central-1a"
      blueprint_id      = "ubuntu_24_04"
      bundle_id         = "micro_3_0"
      ip_address_type   = "ipv4"
    }
  }

  override_resource {
    target = aws_lightsail_static_ip.vm
    values = {
      name       = "game-vm"
      ip_address = "192.0.2.10"
    }
  }

  override_resource {
    target = aws_lightsail_static_ip_attachment.vm
    values = {
      static_ip_name = "game-vm"
      instance_name  = "game-1"
    }
  }

  assert {
    condition     = output.static_ip == "192.0.2.10"
    error_message = "The static IP must be the one imported."
  }

  assert {
    condition     = aws_lightsail_static_ip.vm.name == "game-vm" && aws_lightsail_static_ip_attachment.vm.static_ip_name == "game-vm" && aws_lightsail_instance.vm["game-1"].name == "game-1"
    error_message = "The VM's resources must take the names of the ones they import."
  }

  assert {
    condition     = length(aws_lightsail_instance_public_ports.vm) == 0
    error_message = "A firewall that another configuration manages must be left alone."
  }

}

run "rejects_a_static_ip_instance_that_isnt_listed" {
  command = plan

  variables {
    static_ip_instance = "game-2"
  }

  expect_failures = [var.static_ip_instance]
}

# Backups, alarms and the deploy role --------------------------------------------

run "keeps_backups_from_the_github_roles" {
  command = plan

  override_data {
    target = data.aws_caller_identity.current
    values = {
      account_id = "123456789012"
    }
  }

  override_data {
    target = data.aws_iam_openid_connect_provider.github[0]
    values = {
      arn = "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
    }
  }

  # AWS assigns these on creation.
  override_resource {
    target          = aws_s3_bucket.backups
    override_during = plan
    values = {
      arn = "arn:aws:s3:::mpl-backups-123456789012"
    }
  }

  override_resource {
    target          = aws_iam_role.lambda
    override_during = plan
    values = {
      arn = "arn:aws:iam::123456789012:role/mpl-lambda"
    }
  }

  variables {
    github_repository   = "example/mpl"
    deploy_bucket       = "shared-deploy-123456789012"
    backup_denied_roles = ["game-github-deploy"]
  }

  assert {
    condition     = aws_s3_bucket.backups.bucket == "mpl-backups-123456789012"
    error_message = "The backups bucket must be named mpl-*, which the apply role may change."
  }

  assert {
    condition = (
      toset(one([for statement in jsondecode(aws_s3_bucket_policy.backups.policy).Statement : statement if statement.Sid == "KeepDumpsFromGitHubRoles"]).Condition.ArnEquals["aws:PrincipalArn"]) ==
      toset(["arn:aws:iam::123456789012:role/mpl-github-deploy", "arn:aws:iam::123456789012:role/mpl-github-apply", "arn:aws:iam::123456789012:role/game-github-deploy"])
    )
    error_message = "Dumps must be kept from this repository's GitHub roles and the ones named."
  }

  assert {
    condition = (
      jsondecode(aws_iam_role.github_deploy[0].assume_role_policy).Statement[0].Condition.StringEquals["token.actions.githubusercontent.com:sub"] == "repo:example/mpl:environment:production" &&
      jsondecode(aws_iam_role.github_deploy[0].assume_role_policy).Statement[0].Principal.Federated == "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
    )
    error_message = "Only the production environment's jobs of the repository may assume the deploy role, through the account's GitHub provider."
  }

  assert {
    condition = (
      one([for statement in jsondecode(aws_iam_role_policy.github_deploy[0].policy).Statement : statement if statement.Sid == "SyncItsPartOfTheDeployBucket"]).Resource == "arn:aws:s3:::shared-deploy-123456789012/mpl/*" &&
      toset(one([for statement in jsondecode(aws_iam_role_policy.github_deploy[0].policy).Statement : statement if statement.Sid == "ReleaseTheStateLockAndOldPlans"]).Resource) == toset([
        "arn:aws:s3:::shared-deploy-123456789012/mpl/terraform/terraform.tfstate.tflock",
        "arn:aws:s3:::shared-deploy-123456789012/mpl/plans/*",
      ])
    )
    error_message = "The deploy role may write only the repository's part of the shared deploy bucket."
  }

  assert {
    condition     = one([for statement in jsondecode(aws_iam_role_policy.github_deploy[0].policy).Statement : statement if statement.Sid == "PublishTheLobby"]).Resource == "arn:aws:lambda:eu-central-1:123456789012:function:mpl"
    error_message = "The deploy role may publish the lobby function and no other."
  }
}

run "alarms_on_errors_and_missing_backups" {
  command = plan

  assert {
    condition     = aws_cloudwatch_log_metric_filter.errors.log_group_name == "/aws/lambda/mpl" && aws_cloudwatch_metric_alarm.error_lines.metric_name == "ErrorLines" && aws_cloudwatch_metric_alarm.lambda_errors.dimensions.FunctionName == "mpl"
    error_message = "The alarms must watch the lobby function and its error lines."
  }

  assert {
    condition     = aws_cloudwatch_metric_alarm.database_backups.namespace == "Lobby/VM" && aws_cloudwatch_metric_alarm.database_backups.treat_missing_data == "breaching" && aws_sns_topic_subscription.alert_email.endpoint == "alerts@example.org"
    error_message = "A day without a backup must email alert_email."
  }
}
