# AWS Lightsail Distributed Cache Deployment
# 3-node cluster on Micro plan ($5/month each = $15/month total)

terraform {
  required_version = ">= 1.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

# Use Debian 12 blueprint ( Lightsail managed )
# See: https://docs.aws.amazon.com/lightsail/latest/userguide/amazon-lightsail-available-blueprints.html

# Create SSH key pair
resource "aws_lightsail_key_pair" "cache_key" {
  name = "cache-cluster-key"
}

# Create 3 Lightsail instances (without user_data that references other nodes)
resource "aws_lightsail_instance" "cache_node" {
  count             = 3
  name              = "cache-node-${count.index + 1}"
  availability_zone = "${var.aws_region}a"
  blueprint_id      = "debian_12"  # Debian 12 LTS
  bundle_id         = var.bundle_id
  key_pair_name     = aws_lightsail_key_pair.cache_key.name

  tags = {
    Project = "self-healing-cache"
    Node    = "node-${count.index + 1}"
  }
}

# Create static IPs for each node
resource "aws_lightsail_static_ip" "cache_ip" {
  count = 3
  name  = "cache-node-${count.index + 1}-ip"
}

# Attach static IPs to instances
resource "aws_lightsail_static_ip_attachment" "cache_ip_attach" {
  count          = 3
  static_ip_name = aws_lightsail_static_ip.cache_ip[count.index].name
  instance_name  = aws_lightsail_instance.cache_node[count.index].name
}

# Firewall rules - allow HTTP and gossip protocol
resource "aws_lightsail_instance_public_ports" "cache_ports" {
  count         = 3
  instance_name = aws_lightsail_instance.cache_node[count.index].name

  port_info {
    protocol  = "tcp"
    from_port = 8080
    to_port   = 8080
    cidrs     = ["0.0.0.0/0"]
  }

  port_info {
    protocol  = "tcp"
    from_port = 7946
    to_port   = 7946
    cidrs     = ["0.0.0.0/0"]
  }

  # SWIM gossip probes run over UDP on the same port — without this, nodes
  # stay mutually suspect and the cluster never converges (alive_nodes stuck at 1).
  port_info {
    protocol  = "udp"
    from_port = 7946
    to_port   = 7946
    cidrs     = ["0.0.0.0/0"]
  }

  port_info {
    protocol  = "tcp"
    from_port = 22
    to_port   = 22
    cidrs     = ["0.0.0.0/0"]
  }
}

# Output node IPs for use in setup
locals {
  node_ips = aws_lightsail_static_ip.cache_ip[*].ip_address
}
