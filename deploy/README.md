# AWS Lightsail Deployment

Deploys a 3-node self-healing distributed cache cluster on AWS Lightsail.

## Cost

- **Region:** ap-south-1 (Mumbai)
- **Plan:** Micro ($5/month per node)
- **Total:** $15/month for 3 nodes
- **Specs per node:** 1 vCPU, 1GB RAM, 40GB SSD, 2TB transfer

## Prerequisites

- AWS account with credentials configured
- Terraform installed (>= 1.0)
- AWS CLI configured (`aws configure`)

## Quick Deploy

```bash
cd deploy/

# Initialize Terraform
terraform init

# Preview what will be created
terraform plan

# Deploy
terraform apply

# View outputs (IPs, endpoints)
terraform output
```

## After Deployment

### Test the cluster:

```bash
# Get endpoints
terraform output cluster_endpoints

# Set a key (use any node IP)
curl -X POST http://<NODE_IP>:8080/set \
  -H "Content-Type: application/json" \
  -d '{"key":"hello","value":"world"}'

# Get the key (from any node)
curl http://<NODE_IP>:8080/get?key=hello

# Check ring info
curl http://<NODE_IP>:8080/ring/info
```

### SSH into nodes:

```bash
# Get SSH command
terraform output ssh_command

# Or manually
ssh -i ~/.ssh/<key-name> admin@<NODE_IP>
```

## Cleanup

```bash
# Destroy all resources
terraform destroy
```

## Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Node 1        │◄──►│   Node 2        │◄──►│   Node 3        │
│   (Primary)     │    │   (Replica)     │    │   (Replica)     │
│   Port 8080     │    │   Port 8080     │    │   Port 8080     │
└─────────────────┘    └─────────────────┘    └─────────────────┘
        │                       │                       │
        └───────────────────────┴───────────────────────┘
                    Gossip Mesh (Port 7946)
```

## Files

| File | Purpose |
|------|---------|
| `main.tf` | Main Terraform configuration |
| `variables.tf` | Configurable variables |
| `outputs.tf` | Output values after deploy |
| `user_data.sh` | Node startup script |
