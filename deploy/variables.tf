# Variables for AWS Lightsail deployment

variable "aws_region" {
  description = "AWS region for deployment"
  type        = string
  default     = "ap-south-1"
}

variable "node_count" {
  description = "Number of cache nodes"
  type        = number
  default     = 3
}

variable "bundle_id" {
  description = "Lightsail bundle (instance size)"
  type        = string
  default     = "nano_3_1"  # $5/month: 2 vCPU, 0.5GB RAM, 20GB SSD (ap-south-1)
}

# ap-south-1 bundle options:
# nano_3_1:  $5/mo  - 0.5GB RAM, 20GB SSD (cheapest with IPv4)
# micro_3_1: $7/mo  - 1GB RAM, 40GB SSD
# small_3_1: $12/mo - 2GB RAM, 60GB SSD

variable "docker_image" {
  description = "Docker image for cache server"
  type        = string
  default     = "ghcr.io/noblechicken97/self-healing-distributed-cache:latest"
}
