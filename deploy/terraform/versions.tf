terraform {
  # 1.7 for import blocks with for_each, and 1.11 for the S3 backend's
  # lock file.
  required_version = ">= 1.11"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
    archive = {
      source  = "hashicorp/archive"
      version = "~> 2.7"
    }
  }

  # The state lives in the deploy bucket, under mpl/, beside the state of any
  # other deployment that shares the bucket. scripts/aws.sh passes the bucket
  # and its region (deploy/deploy_files.py). The lock file stops two applies
  # at once. The state holds the Lambda's environment, secrets included.
  backend "s3" {
    key          = "mpl/terraform/terraform.tfstate"
    encrypt      = true
    use_lockfile = true
  }
}

provider "aws" {
  region = var.region

  default_tags {
    tags = {
      Project   = var.name
      ManagedBy = "terraform"
    }
  }
}

# The VM's resources (vm.tf) get their own tags, so that taking over a VM
# that another configuration created keeps the tags it has: different
# default tags would show as a change to the instance in the first plan.
provider "aws" {
  alias  = "vm"
  region = var.region

  default_tags {
    tags = var.vm_tags
  }
}
