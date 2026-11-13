#!/usr/bin/env bash
# Create the AWS resources with Terraform, or deploy a new lobby version to
# Lambda. See deploy/README.md.
set -euo pipefail
repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
terraform_directory="$repository_root/deploy/terraform"
local_directory="$repository_root/deploy/local"
variables_file="$local_directory/aws.tfvars"
lambda_settings_file="$local_directory/lobby.env"
build_directory="$local_directory/lambda"
plan_file="$local_directory/terraform/scripts-aws.tfplan"
backup_settings_file="$local_directory/secrets/database_backups.json"

usage() {
  cat >&2 <<'EOF'
Terraform's state is in the deploy bucket that MPL_DEPLOY_BUCKET names, under
mpl/. apply and deploy pull deploy/local/ from the bucket first and push it
back at the end.

Usage:
  scripts/aws.sh apply [terraform plan options]
      Build the lobby, then create or update every AWS resource: the
      Lightsail VM, its static IP and firewall, the lobby Lambda, its
      certificate and CloudFront distribution, the database backups' bucket
      and key, the alarms and the deploy role. Shows the plan and asks
      first. Refuses a plan that imports anything with a change
      (deploy/README.md, "Taking over a running VM"). Writes the backups' key
      to deploy/local/secrets/database_backups.json for the VM deploy. A
      plan that deletes or replaces an instance, the static IP, a disk or the
      distribution also needs each one's name typed, or listed in
      MPL_ALLOW_DESTROY, comma-separated. Runs the lobby's migrations before
      publishing new code, once the VM deploy has written
      deploy/local/lobby.env. The first apply needs the certificate alone,
      -target=aws_acm_certificate.lobby, so its validation record can be
      added before the full apply waits for it. The very first full apply
      needs -var allow_missing_lambda_settings=true.
  scripts/aws.sh deploy
      Build the lobby, run its migrations against Postgres and publish the new
      code and deploy/local/lobby.env's settings to the Lambda. Doesn't ask,
      and refuses a plan that imports, changes Lightsail resources or the
      lobby's certificate or distribution, or deletes anything. Never
      touches the VM.
  scripts/aws.sh plan <directory> [terraform plan options]
      Build the lobby and plan, like apply, without asking or applying.
      Refuses a plan that imports anything with a change. Saves the plan,
      the build it packages and the plan as text in the directory, which
      then holds the Lambda's settings. The Deploy workflow's plan target
      runs it.
  scripts/aws.sh apply-plan <directory>
      Apply a plan that plan saved, without asking: the Deploy workflow's
      apply target runs it on main, once someone has read the plan. Refuses
      a plan that imports anything with a change, or that destroys an
      instance, the static IP, a disk or the distribution unless
      MPL_ALLOW_DESTROY lists it. Runs the migrations first when the plan
      publishes the lobby.
  scripts/aws.sh output [name]
      Print Terraform's outputs, or one of them, e.g. static_ip,
      function_url, lobby_url, certificate_validation_record,
      lobby_dns_record or distribution_domain.
EOF
  exit 2
}

if [[ $# -lt 1 ]]; then
  usage
fi
command="$1"
shift

require_command() {
  if ! command -v "$1" >/dev/null; then
    echo "Install $1 first. See deploy/README.md." >&2
    exit 1
  fi
}

run_terraform() {
  terraform -chdir="$terraform_directory" "$@"
}

deploy_files() {
  python3 -I "$repository_root/deploy/deploy_files.py" "$@"
}

# The bucket comes from MPL_DEPLOY_BUCKET and its region from the bucket, so a
# new machine needs nothing else. Terraform's GitHub deploy role is scoped to
# the bucket's mpl/.
initialize_terraform() {
  require_command terraform
  require_command aws
  require_command python3
  mkdir -p "$local_directory/terraform"
  local bucket_and_region bucket region
  bucket_and_region="$(deploy_files bucket)"
  read -r bucket region <<<"$bucket_and_region"
  export TF_VAR_deploy_bucket="$bucket"
  # .terraform/terraform.tfstate records the backend and its bucket; `init
  # -backend=false` (for validating) leaves it out.
  if ! python3 -I -c 'import json, sys
backend = json.load(open(sys.argv[1])).get("backend") or {}
sys.exit(0 if backend.get("type") == "s3" and backend["config"].get("bucket") == sys.argv[2] else 1)' \
    "$terraform_directory/.terraform/terraform.tfstate" "$bucket" 2>/dev/null; then
    # To stderr, so "$(scripts/aws.sh output static_ip)" captures only the IP.
    run_terraform init -input=false -reconfigure \
      -backend-config="bucket=$bucket" -backend-config="region=$region" >&2
  fi
}

# Removes the plan, which holds the Lambda's settings, and pushes what changed
# in deploy/local/, keeping the run's own exit status.
finish() {
  local status=$?
  rm -f "$plan_file"
  if ! deploy_files push; then
    echo "deploy/local/ isn't in the deploy bucket yet. Run scripts/deploy-files.sh push." >&2
    [[ $status -ne 0 ]] || status=1
  fi
  exit "$status"
}

sync_deploy_files() {
  require_command aws
  require_command python3
  deploy_files pull
  trap finish EXIT
}

require_variables() {
  if [[ ! -f "$variables_file" ]]; then
    echo "Copy deploy/aws.example.tfvars to deploy/local/aws.tfvars and fill it in." >&2
    exit 1
  fi
}

# The build command from cmd/lobby-lambda. Terraform zips the result.
build_lobby() {
  require_command go
  mkdir -p "$build_directory"
  echo "Building the lobby for Lambda..."
  (cd "$repository_root" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
    go build -tags lambda.norpc -trimpath -ldflags="-s -w" -o "$build_directory/bootstrap" ./cmd/lobby-lambda)
}

# The builds a plan packages: the lobby, and the zip Terraform writes while it
# plans.
saved_builds=(bootstrap bootstrap.zip)

# Reads one KEY=value line from lobby.env without evaluating it as shell. Like
# Terraform (lambda.tf), it ignores spaces and a carriage return around the
# line, and keeps the last value of a repeated key.
lambda_setting() {
  sed -n -E "s/^[[:space:]]*$1=(.*[^[:space:]])?[[:space:]]*\$/\1/p" "$lambda_settings_file" | tail -n 1
}

# Migrations run from this machine, straight against Postgres over TLS, before
# the new code is published. A cold start runs no DDL. The tenants migrate
# their own databases.
migrate_database() {
  local database_url
  database_url="$(lambda_setting LOBBY_DATABASE_URL)"
  if [[ -z "$database_url" ]]; then
    echo "$lambda_settings_file lacks LOBBY_DATABASE_URL. Run the VM deploy again to rewrite it." >&2
    exit 1
  fi
  echo "Migrating the lobby's database..."
  (cd "$repository_root" && LOBBY_DATABASE_URL="$database_url" go run ./cmd/migrate)
}

check_plan() {
  run_terraform show -json "$plan_file" | python3 -I "$repository_root/deploy/check_deploy_plan.py" "$1"
}

# Deleting or replacing an instance deletes its disk and everything written
# since the last backup, releasing the static IP gives vm.<domain>'s address
# away, and a new distribution has a new domain for the lobby's CNAME. A
# generic yes isn't enough for that: each one's name must be typed, or listed
# in MPL_ALLOW_DESTROY.
confirm_destruction() {
  local destroyed name answer
  local names=()
  destroyed="$(check_plan apply)"
  [[ -n "$destroyed" ]] || return 0
  while IFS= read -r name; do
    names+=("$name")
  done <<<"$destroyed"
  local allowed=",${MPL_ALLOW_DESTROY:-},"
  for name in "${names[@]}"; do
    if [[ "$allowed" == *",$name,"* ]]; then
      echo "MPL_ALLOW_DESTROY allows destroying $name." >&2
      continue
    fi
    read -r -p "Type $name to destroy it: " answer
    if [[ "$answer" != "$name" ]]; then
      echo "Nothing applied. Check deploy/local/aws.tfvars against the resources that run." >&2
      exit 1
    fi
  done
}

# The VM deploy installs the daily backup's bucket and upload-only key on the
# VM (deploy/README.md, "Backups"). Only Terraform's state has the key, so
# every apply copies it to deploy/local/secrets/, which the deploy bucket
# shares. An apply of the certificate alone has no such output yet.
save_backup_settings() {
  local settings
  if ! settings="$(run_terraform output -json database_backups 2>/dev/null)" || [[ -z "$settings" ]] || [[ "$settings" == null ]]; then
    return 0
  fi
  mkdir -p "$(dirname -- "$backup_settings_file")"
  (umask 077 && printf '%s\n' "$settings" >"$backup_settings_file")
}

# The Deploy workflow's plan target saves a plan, and its apply target applies
# it in a later run, once someone has read it. The plan packages
# deploy/local/lambda/bootstrap.zip by its hash, so the apply needs that same
# file, not a new build.
save_plan() {
  local directory="$1"
  shift
  initialize_terraform
  require_variables
  build_lobby
  run_terraform plan -input=false -var-file="$variables_file" -out="$plan_file" "$@"
  # A takeover whose plan would change the VM stops here, before anyone can
  # apply it.
  check_plan imports
  mkdir -p "$directory"
  cp "$plan_file" "$directory/tfplan"
  local build
  for build in "${saved_builds[@]}"; do
    cp -R "$build_directory/$build" "$directory/"
  done
  run_terraform show -no-color "$plan_file" >"$directory/plan.txt"
  # One name per line; the descriptions go to stderr, into the log.
  check_plan apply >"$directory/destroys"
  check_plan publishes-lobby >"$directory/publishes-lobby"
}

apply_saved_plan() {
  local directory="$1" name
  initialize_terraform
  mkdir -p "$build_directory" "$(dirname -- "$plan_file")"
  local build
  for build in "${saved_builds[@]}"; do
    rm -rf "${build_directory:?}/$build"
    cp -R "$directory/$build" "$build_directory/"
  done
  cp "$directory/tfplan" "$plan_file"
  check_plan imports
  # Assigned first, so a failing check stops the script.
  local destroyed allowed=",${MPL_ALLOW_DESTROY:-},"
  local refused=()
  destroyed="$(check_plan apply)"
  while IFS= read -r name; do
    [[ -n "$name" ]] || continue
    if [[ "$allowed" != *",$name,"* ]]; then
      refused+=("$name")
    fi
  done <<<"$destroyed"
  if [[ ${#refused[@]} -gt 0 ]]; then
    echo "The plan destroys ${refused[*]}. Nothing applied. If that's intended, start the run again with allow_destroy listing each name." >&2
    exit 1
  fi
  if [[ "$(check_plan publishes-lobby)" == yes ]]; then
    if [[ -f "$lambda_settings_file" ]]; then
      migrate_database
    else
      echo "No $lambda_settings_file yet, so no migrations."
    fi
  fi
  run_terraform apply -input=false "$plan_file"
  save_backup_settings
}

# Plans, checks the plan, migrates and applies exactly the plan that was
# checked. The plan file holds the Lambda's settings, so finish removes it.
plan_and_apply() {
  local mode="$1"
  shift
  initialize_terraform
  require_variables
  build_lobby
  run_terraform plan -input=false -var-file="$variables_file" -out="$plan_file" "$@"

  local migrate=yes
  if [[ "$mode" == deploy ]]; then
    check_plan deploy
  else
    check_plan imports
    confirm_destruction
    local answer
    read -r -p "Apply this plan? Only 'yes' applies it: " answer
    if [[ "$answer" != yes ]]; then
      echo "Nothing applied." >&2
      exit 1
    fi
    # Only migrate when this apply publishes the lobby.
    migrate="$(check_plan publishes-lobby)"
  fi

  if [[ "$migrate" == yes ]]; then
    if [[ -f "$lambda_settings_file" ]]; then
      migrate_database
    else
      echo "No $lambda_settings_file yet, so no migrations: the Lambda gets its settings on the first scripts/aws.sh deploy."
    fi
  fi
  run_terraform apply -input=false "$plan_file"
  save_backup_settings
  # The certificate's apply on its own leaves the rest for later.
  local function_url
  if function_url="$(run_terraform output -raw function_url 2>/dev/null)"; then
    echo "The lobby's Function URL is $function_url"
  fi
  if run_terraform output -raw distribution_domain >/dev/null 2>&1; then
    echo "Players and games reach the lobby at $(run_terraform output -raw lobby_url)"
  fi
}

case "$command" in
  apply)
    sync_deploy_files
    plan_and_apply apply "$@"
    ;;
  deploy)
    [[ $# -eq 0 ]] || usage
    sync_deploy_files
    if [[ ! -f "$lambda_settings_file" ]]; then
      echo "$lambda_settings_file is missing. Push deploy/local/ from the machine that has it, or run scripts/vm.sh deploy, which writes it." >&2
      exit 1
    fi
    plan_and_apply deploy
    ;;
  plan)
    [[ $# -ge 1 ]] || usage
    sync_deploy_files
    save_plan "$@"
    ;;
  apply-plan)
    [[ $# -eq 1 ]] || usage
    [[ -f "$1/tfplan" ]] || { echo "$1 has no saved plan. Run scripts/aws.sh plan $1 first." >&2; exit 1; }
    sync_deploy_files
    apply_saved_plan "$1"
    ;;
  output)
    [[ $# -le 1 ]] || usage
    initialize_terraform
    if [[ $# -eq 1 ]]; then
      run_terraform output -raw "$1"
    else
      run_terraform output
    fi
    ;;
  *)
    usage
    ;;
esac
