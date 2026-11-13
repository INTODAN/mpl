# Settings come from deploy/local/aws.tfvars, which scripts/aws.sh passes with
# -var-file. See deploy/aws.example.tfvars and deploy/README.md.

variable "region" {
  description = "The AWS region for the VM and the Lambda."
  type        = string

  validation {
    condition     = can(regex("^[a-z]{2}(-[a-z]+)+-[0-9]+$", var.region))
    error_message = "region must be an AWS region, such as eu-central-1."
  }
}

variable "name" {
  description = "Prefix for the names of the Lambda, its roles, the backups bucket, the alarms and, by default, the static IP. It is also the Project tag of everything but the VM."
  type        = string
  default     = "mpl"

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,30}[a-z0-9]$", var.name))
    error_message = "name must be lowercase letters, digits and hyphens, such as mpl."
  }
}

variable "lobby_hostname" {
  description = <<-EOT
    The lobby's own name, such as lobby.example.org. CloudFront serves it
    from the Function URL, with an ACM certificate. Its CNAME and the
    certificate's validation record are added to the domain's DNS by hand,
    from the outputs (deploy/README.md).
  EOT
  type        = string

  validation {
    condition     = can(regex("^([a-z0-9]([a-z0-9-]*[a-z0-9])?\\.)+[a-z]{2,}$", var.lobby_hostname))
    error_message = "lobby_hostname must be a lowercase hostname, such as lobby.example.org."
  }
}

# The VM --------------------------------------------------------------------

variable "vm_instances" {
  description = <<-EOT
    The Lightsail instances, by name. Normally one. Moving to a new VM adds a
    second one next to it, moves the static IP and then removes the old one
    (deploy/README.md). Changing an existing instance's zone, image or plan
    replaces it and deletes its disk, so add a new instance instead. When
    taking over a running VM, list it exactly as it runs. There's no
    default, so a lost or stale aws.tfvars can't plan to replace the live
    instance with a default one.
  EOT
  type = map(object({
    # Defaults to the region's "a" zone.
    availability_zone = optional(string)
    # Ubuntu 24.04 LTS. The VM deploy needs 24.04 or newer.
    blueprint_id = optional(string, "ubuntu_24_04")
    # 1 GB of memory, 2 vCPUs, 40 GB of disk and a public IPv4 address.
    bundle_id = optional(string, "micro_3_0")
  }))

  validation {
    condition     = length(var.vm_instances) > 0
    error_message = "List at least one instance in vm_instances."
  }
}

variable "static_ip_instance" {
  description = "The instance in vm_instances the static IP, and so vm.<domain>, points at."
  type        = string

  validation {
    condition     = contains(keys(var.vm_instances), var.static_ip_instance)
    error_message = "static_ip_instance must name an instance in vm_instances."
  }
}

variable "static_ip_name" {
  description = "The static IP's name. Defaults to <name>-vm. When taking over a running VM, the name of its static IP."
  type        = string
  default     = null
}

variable "adopt_vm" {
  description = <<-EOT
    Take over a VM that runs already: import every instance in vm_instances,
    the static IP and its attachment into this state, unchanged
    (deploy/README.md, "Taking over a running VM"). The plan must import
    them with no change; deploy/check_deploy_plan.py refuses one that would
    change them. Set it to false after the apply that imported them, and for
    a new deployment, whose VM this configuration creates. There's no
    default, so the choice is always made on purpose.
  EOT
  type        = bool
}

variable "manage_vm_firewall" {
  description = <<-EOT
    Manage the instances' firewall (vm.tf). Lightsail can't import a
    firewall, so keep it false while another configuration still manages
    the firewall of a VM taken over, and set it to true once that one has
    let go of it (deploy/README.md, "The cutover").
  EOT
  type        = bool
  default     = true
}

variable "vm_tags" {
  description = <<-EOT
    The tags of the VM's instances. Other resources get Project = name. When
    taking over a running VM, the tags it has, so the import changes nothing,
    such as { Project = "<the old project>", ManagedBy = "terraform" }.
  EOT
  type        = map(string)
  default     = { ManagedBy = "terraform" }
}

variable "vm_key_pair_name" {
  description = <<-EOT
    An existing Lightsail key pair whose key logs in to new instances as
    `ubuntu`. Null uses the region's default key pair. Deploys don't need
    it: they log in with Lightsail's temporary keys. Instances that exist
    keep the key pair they were created with.
  EOT
  type        = string
  default     = null
}

variable "ssh_allowed_cidrs" {
  description = "IPv4 ranges that may reach SSH (22). Deploys log in with Lightsail's temporary keys from wherever they run, a GitHub runner included, so keep it open to all. Password logins are off."
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "postgres_port" {
  description = "Postgres's public port. It must match the VM deploy's postgres_port."
  type        = number
  default     = 5432
}

variable "database_backup_retention_days" {
  description = "How many days S3 keeps each daily database backup (backups.tf), and each overwritten one."
  type        = number
  default     = 7

  validation {
    condition     = var.database_backup_retention_days >= 1 && var.database_backup_retention_days == floor(var.database_backup_retention_days)
    error_message = "database_backup_retention_days must be a whole number of days, at least 1."
  }
}

variable "backup_denied_roles" {
  description = "Names of further IAM roles that may never download a database backup, such as another repository's GitHub roles, whose ReadOnlyAccess would otherwise let them read the dumps."
  type        = list(string)
  default     = []
}

# The Lambda ---------------------------------------------------------------

variable "lambda_memory_mb" {
  description = "The Lambda's memory. CPU scales with it."
  type        = number
  default     = 512
}

variable "lambda_timeout_seconds" {
  description = "How long one request may run. The lobby ends a request's work 2 seconds before it (cmd/lobby-lambda), so a timeout doesn't reset the instance. At most 50, so a request and a cold start fit in CloudFront's 60-second origin timeout (cloudfront.tf)."
  type        = number
  default     = 30

  validation {
    condition     = var.lambda_timeout_seconds > 2 && var.lambda_timeout_seconds <= 50
    error_message = "lambda_timeout_seconds must be more than the lobby's 2-second deadline margin, and at most 50, so CloudFront's 60-second origin timeout doesn't cut a request short and retry it (cloudfront.tf)."
  }
}

variable "reserved_concurrency" {
  description = <<-EOT
    The most Lambda instances that run at once. Every instance keeps its own
    pool of up to database_max_open_connections to the lobby's database, so
    the product, with tenant_database_connections, must leave
    postgres_spare_connections free under postgres_max_connections.
  EOT
  type        = number
  default     = 5

  validation {
    condition     = var.reserved_concurrency >= 1 && floor(var.reserved_concurrency) == var.reserved_concurrency
    error_message = "reserved_concurrency must be a whole number of at least 1. 0 would stop the function."
  }
}

variable "database_max_open_connections" {
  description = "LOBBY_DATABASE_MAX_OPEN_CONNECTIONS for every Lambda instance (README.md, \"Running the lobby\")."
  type        = number
  default     = 2

  validation {
    condition     = var.database_max_open_connections >= 1
    error_message = "database_max_open_connections must be at least 1."
  }
}

variable "tenant_database_connections" {
  description = <<-EOT
    The most connections that the tenants, the games with a database on the
    VM, open together: for a game on Lambda, its concurrency × its pools ×
    its pool size. The default fits one game of 5 instances with two pools
    of 2 each.
  EOT
  type        = number
  default     = 20

  validation {
    condition     = var.tenant_database_connections >= 0
    error_message = "tenant_database_connections can't be negative."
  }
}

variable "postgres_max_connections" {
  description = "Postgres's max_connections on the VM. The VM deploy keeps Postgres's default of 100."
  type        = number
  default     = 100
}

variable "postgres_spare_connections" {
  description = <<-EOT
    Connections that the Lambda and the tenants must leave free: 3 that
    Postgres reserves for superusers, 2 for the migrate commands, and some
    for administration.
  EOT
  type        = number
  default     = 10

  validation {
    condition     = var.postgres_spare_connections >= 5
    error_message = "postgres_spare_connections must be at least 5: 3 for superusers and 2 for the migrate commands."
  }
}

variable "lambda_settings_file" {
  description = <<-EOT
    The Lambda's settings, which the VM deploy writes. Every KEY=value line is
    passed to the function as an environment variable. Defaults to
    deploy/local/lobby.env.
  EOT
  type        = string
  default     = null
}

variable "allow_missing_lambda_settings" {
  description = <<-EOT
    Create the function without lambda_settings_file. Only for the very first
    apply, before the VM deploy has written it: the function then answers 503
    until scripts/aws.sh deploy publishes its settings.
  EOT
  type        = bool
  default     = false
}

variable "extra_lambda_environment" {
  description = "Further environment variables for the function, such as LOBBY_RETENTION_IDLE_PERIOD. Keep secrets in lambda_settings_file instead."
  type        = map(string)
  default     = {}
}

variable "lambda_bootstrap_file" {
  description = "The built Lambda executable. Defaults to deploy/local/lambda/bootstrap, where scripts/aws.sh builds it."
  type        = string
  default     = null
}

# Alarms and the deploy workflow ---------------------------------------------

variable "alert_email" {
  description = <<-EOT
    Where the error and backup alarms send their emails. Set it in
    deploy/local/aws.tfvars and never commit it. Confirm the subscription
    email that SNS sends after the first apply, or the alarms send nothing.
  EOT
  type        = string

  validation {
    condition     = can(regex("^[^@[:space:]]+@[^@[:space:]]+\\.[^@[:space:]]+$", var.alert_email))
    error_message = "alert_email must be an email address."
  }
}

variable "deploy_bucket" {
  description = "The deploy bucket, which holds the state and deploy/local/'s files under mpl/. scripts/aws.sh sets it from MPL_DEPLOY_BUCKET (deploy/deploy_files.py)."
  type        = string
  default     = null
}

variable "github_repository" {
  description = <<-EOT
    The GitHub repository, as owner/name, whose Deploy workflow may deploy
    through an IAM role it assumes with OIDC (github.tf). Null creates no role.
  EOT
  type        = string
  default     = null

  validation {
    condition     = var.github_repository == null || can(regex("^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$", var.github_repository))
    error_message = "github_repository must be owner/name, such as INTODAN/mpl."
  }

  validation {
    condition     = var.github_repository == null || var.deploy_bucket != null
    error_message = "The GitHub deploy role needs deploy_bucket, which scripts/aws.sh sets."
  }
}

variable "github_environment" {
  description = "The GitHub environment whose jobs may assume the deploy role. Limit it to the main branch in the repository's settings."
  type        = string
  default     = "production"
}
