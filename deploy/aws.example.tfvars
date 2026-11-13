# Settings for scripts/aws.sh. Copy this file to deploy/local/aws.tfvars, which
# is gitignored and kept in the deploy bucket under mpl/files/, and replace the
# placeholders. Every variable is described in deploy/terraform/variables.tf.
# See deploy/README.md.

# The AWS region of the VM and the Lambda.
region = "<region, e.g. eu-central-1>"

# The lobby's own name. CloudFront serves it with an ACM certificate, and its
# DNS records are added by hand from the outputs (deploy/README.md).
lobby_hostname = "lobby.<your-domain>"

# Where the error and backup alarms send their emails.
alert_email = "<your email address>"

# Let the repository's Deploy workflow deploy through an IAM role that GitHub
# Actions assumes with OIDC (deploy/README.md).
github_repository = "<owner>/mpl"

# The VM ----------------------------------------------------------------------
#
# Taking over a VM that runs already (deploy/README.md, "Taking over a running
# VM"): describe it exactly as it runs, so the first plan imports it with no
# change. Look the values up in the configuration that created it, or with
# `aws lightsail get-instances` and `aws lightsail get-static-ips`.
adopt_vm = true
vm_instances = {
  "<instance name>" = {
    # Only if they differ from the defaults: the region's "a" zone,
    # ubuntu_24_04 and micro_3_0.
    # availability_zone = "<zone>"
    # blueprint_id      = "<blueprint>"
    # bundle_id         = "<bundle>"
  }
}
static_ip_instance = "<instance name>"
static_ip_name     = "<static IP name>"
# The instance's tags as they are, such as the other configuration's default
# tags.
vm_tags = {
  Project   = "<the other project's tag>"
  ManagedBy = "terraform"
}
# The other configuration keeps the firewall until the cutover.
manage_vm_firewall = false
# Its GitHub roles may not read this deployment's backups.
# backup_denied_roles = ["<other>-github-deploy", "<other>-github-apply"]

# A new deployment instead: this configuration creates the VM.
# adopt_vm = false
# vm_instances = {
#   "mpl-1" = {}
# }
# static_ip_instance = "mpl-1"

# Optional: the most connections the tenants open together (default 20).
# tenant_database_connections = 20

# Optional: further, non-secret environment variables for the Lambda.
# extra_lambda_environment = {
#   LOBBY_RETENTION_IDLE_PERIOD = "2h"
# }
