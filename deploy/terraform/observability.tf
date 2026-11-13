# Logs and error alarms for the lobby Lambda, within AWS's free monthly
# allowances (deploy/README.md, "Logs and alarms").
#
# Never turn on Lambda Active tracing, Lambda Insights, CloudWatch
# Transaction Search, Application Signals, RUM, Synthetics, Contributor
# Insights or detailed monitoring: each one is billed outside these limits.
# The account's cost budgets belong to the account, not to this
# configuration: one per account is enough, and a second set would only send
# every email twice.

# Without this, the function creates its own log group on first use and keeps
# the logs forever. Seven days keep storage small.
resource "aws_cloudwatch_log_group" "lobby" {
  name              = "/aws/lambda/${var.name}"
  retention_in_days = 7
  # Metric filters only work on the standard class.
  log_group_class = "STANDARD"
}

# Counts the lobby's error lines. It is 1 of the 10 free custom metrics. It
# has no dimensions, because each dimension value would be another metric.
resource "aws_cloudwatch_log_metric_filter" "errors" {
  name           = "${var.name}-errors"
  log_group_name = aws_cloudwatch_log_group.lobby.name
  pattern        = "{ $.level = \"ERROR\" }"

  metric_transformation {
    name      = "ErrorLines"
    namespace = "Lobby/Lambda"
    value     = "1"
  }
}

resource "aws_sns_topic" "alerts" {
  name = "${var.name}-alerts"
}

# SNS emails a confirmation link first. Until it is followed, the alarms send
# nothing.
resource "aws_sns_topic_subscription" "alert_email" {
  topic_arn = aws_sns_topic.alerts.arn
  protocol  = "email"
  endpoint  = var.alert_email
}

# Two standard alarms of the 10 free (backups.tf has a third). Each looks at
# one hour and notifies only on entering ALARM. It must pass an hour in OK
# first, so each sends at most 372 emails in a 31-day month.
resource "aws_cloudwatch_metric_alarm" "lambda_errors" {
  alarm_name          = "${var.name}-lambda-errors"
  alarm_description   = "The lobby Lambda failed an invocation: a crash, a timeout or a failed start. Read /aws/lambda/${var.name} (deploy/README.md)."
  namespace           = "AWS/Lambda"
  metric_name         = "Errors"
  dimensions          = { FunctionName = aws_lambda_function.lobby.function_name }
  statistic           = "Sum"
  period              = 3600
  evaluation_periods  = 1
  threshold           = 0
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alerts.arn]
}

resource "aws_cloudwatch_metric_alarm" "error_lines" {
  alarm_name          = "${var.name}-error-lines"
  alarm_description   = "The lobby logged an error, such as a failed request or a game hook that failed. Read /aws/lambda/${var.name} (deploy/README.md)."
  namespace           = aws_cloudwatch_log_metric_filter.errors.metric_transformation[0].namespace
  metric_name         = aws_cloudwatch_log_metric_filter.errors.metric_transformation[0].name
  statistic           = "Sum"
  period              = 3600
  evaluation_periods  = 1
  threshold           = 0
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alerts.arn]
}
