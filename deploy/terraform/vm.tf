# The Lightsail VM that runs Postgres and Centrifugo for the lobby and its
# tenants. Ansible configures it: scripts/vm.sh deploy "$(scripts/aws.sh
# output static_ip)" (deploy/README.md).
#
# A VM that another configuration created, such as the one a game's
# repository ran before the lobby moved out, is taken over with the import
# blocks below and adopt_vm = true. The first plan must import every
# instance, the static IP and its attachment with no change, and
# deploy/check_deploy_plan.py refuses one that would change them.

locals {
  static_ip_name = coalesce(var.static_ip_name, "${var.name}-vm")
}

resource "aws_lightsail_instance" "vm" {
  provider = aws.vm
  for_each = var.vm_instances

  name              = each.key
  availability_zone = coalesce(each.value.availability_zone, "${var.region}a")
  blueprint_id      = each.value.blueprint_id
  bundle_id         = each.value.bundle_id
  # Null uses the region's default key pair. Deploys don't need it: they log
  # in with Lightsail's temporary keys (scripts/vm.sh).
  key_pair_name = var.vm_key_pair_name
  # vm.<domain> is only an A record, so the instance needs no IPv6 address and
  # its firewall only has IPv4 rules.
  ip_address_type = "ipv4"

  # No automatic snapshots: the databases are backed up to S3 instead
  # (backups.tf). Lightsail keeps listing a disabled add-on, so it stays
  # declared as Disabled: without the block, every plan would want to change
  # the instance. snapshot_time is ignored.
  add_on {
    type          = "AutoSnapshot"
    snapshot_time = "01:00"
    status        = "Disabled"
  }

  lifecycle {
    # Changing the key pair would replace the instance and delete its disk.
    # A key pair only applies to new instances, and one can't be imported, so
    # a taken-over instance keeps the one it was created with.
    ignore_changes = [key_pair_name]
  }
}

# Lightsail's firewall is the only one: ports that Docker publishes bypass a
# host firewall. These rules replace every other rule on the instance.
#
# Lightsail can't import a firewall, so while another configuration still
# manages a taken-over VM's firewall, manage_vm_firewall stays false and the
# rules stay as that configuration set them (deploy/README.md, "Taking over
# a running VM").
resource "aws_lightsail_instance_public_ports" "vm" {
  provider = aws.vm
  for_each = var.manage_vm_firewall ? aws_lightsail_instance.vm : {}

  instance_name = each.value.name

  port_info {
    protocol  = "tcp"
    from_port = 22
    to_port   = 22
    cidrs     = var.ssh_allowed_cidrs
  }

  # Centrifugo: browsers' WebSocket connections, the lobby's and the games'
  # HTTP API calls, and Let's Encrypt's TLS-ALPN challenge. Port 80 stays
  # closed.
  port_info {
    protocol  = "tcp"
    from_port = 443
    to_port   = 443
    cidrs     = ["0.0.0.0/0"]
  }

  # Postgres, TLS only. Lambda's outbound addresses aren't fixed, so it's open
  # to everyone, and pg_hba.conf admits only each database's own role and the
  # read-only admin role over TLS, each with its password.
  port_info {
    protocol  = "tcp"
    from_port = var.postgres_port
    to_port   = var.postgres_port
    cidrs     = ["0.0.0.0/0"]
  }
}

# The static IP outlives the instances: vm.<domain> points at it, and moving
# to a new VM moves it to the new instance.
resource "aws_lightsail_static_ip" "vm" {
  provider = aws.vm
  name     = local.static_ip_name

  lifecycle {
    # Releasing it gives the address away for good, and vm.<domain> with it.
    prevent_destroy = true
  }
}

resource "aws_lightsail_static_ip_attachment" "vm" {
  provider       = aws.vm
  static_ip_name = aws_lightsail_static_ip.vm.name
  instance_name  = aws_lightsail_instance.vm[var.static_ip_instance].name
}

# Taking over a running VM. Each block imports an existing object into this
# state the first time it's planned; once the object is in the state, the
# block does nothing. Set adopt_vm back to false after that first apply, before
# adding an instance: an instance that doesn't exist yet can't be imported.
import {
  for_each = var.adopt_vm ? var.vm_instances : {}
  provider = aws.vm
  to       = aws_lightsail_instance.vm[each.key]
  id       = each.key
}

import {
  for_each = var.adopt_vm ? toset([local.static_ip_name]) : toset([])
  provider = aws.vm
  to       = aws_lightsail_static_ip.vm
  id       = each.value
}

# The attachment's ID is the static IP's name.
import {
  for_each = var.adopt_vm ? toset([local.static_ip_name]) : toset([])
  provider = aws.vm
  to       = aws_lightsail_static_ip_attachment.vm
  id       = each.value
}
