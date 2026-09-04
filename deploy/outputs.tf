# Output values after deployment

output "node_details" {
  description = "Details of all cache nodes"
  value = {
    for i, node in aws_lightsail_instance.cache_node : node.name => {
      public_ip  = aws_lightsail_static_ip.cache_ip[i].ip_address
      private_ip = node.private_ip_address
      port       = 8080
    }
  }
}

output "cluster_endpoints" {
  description = "HTTP endpoints for the cache cluster"
  value = [
    for i, ip in aws_lightsail_static_ip.cache_ip : "http://${ip.ip_address}:8080"
  ]
}

output "ssh_command" {
  description = "Command to SSH into nodes"
  value = [
    for node in aws_lightsail_instance.cache_node : "ssh -i ~/.ssh/cache-cluster-key admin@${node.public_ip_address}"
  ]
}

output "monthly_cost" {
  description = "Estimated monthly cost (ap-south-1 pricing)"
  value       = "$15/month (3 × $5/month Nano plan) - 0.5GB RAM per node"
}

output "pricing_note" {
  description = "Pricing information"
  value       = "ap-south-1: Nano=$5/mo (0.5GB), Micro=$7/mo (1GB), Small=$12/mo (2GB)"
}
