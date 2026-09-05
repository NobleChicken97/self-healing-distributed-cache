# CI/CD Pipeline Setup Guide

This document explains how to set up the complete CI/CD pipeline:
**GitHub Push → GitHub Actions → ECR → Lightsail Auto-Deploy**

---

## Architecture

```
┌─────────────┐     ┌─────────────────┐     ┌─────────────┐     ┌─────────────┐
│  Git Push   │────▶│  GitHub Actions │────▶│     ECR     │────▶│  Lightsail  │
│  (main/v*)  │     │  Build & Test   │     │  (Docker)   │     │  (3 nodes)  │
└─────────────┘     └─────────────────┘     └─────────────┘     └─────────────┘
```

---

## Prerequisites

1. AWS Account with ECR and Lightsail access
2. GitHub repository with Actions enabled
3. Terraform infrastructure already deployed

---

## Step 1: Create ECR Repository

```bash
cd deploy/
terraform apply -target=aws_ecr_repository.cache
```

Get the repository URL:
```bash
terraform output ecr_repository_url
```

---

## Step 2: Create GitHub OIDC Provider for AWS

This allows GitHub Actions to push to ECR without storing AWS credentials.

### Via AWS Console:

1. Go to **IAM → Identity providers → Add provider**
2. Select **OpenID Connect**
3. Provider URL: `https://token.actions.githubusercontent.com`
4. Audience: `sts.amazonaws.com`

### Via AWS CLI:

```bash
# Create OIDC provider
aws iam create-open-id-connect-provider \
  --url https://token.actions.githubusercontent.com \
  --thumbprint-list 6938fd4d98bab03faadb97b34396831e3780aea1 \
  --client-id-list sts.amazonaws.com
```

---

## Step 3: Create IAM Role for GitHub Actions

Create a role that GitHub Actions can assume to push to ECR.

### Trust Policy (`github-oidc-trust.json`):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::YOUR_ACCOUNT_ID:oidc-provider/token.actions.githubusercontent.com"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
        },
        "StringLike": {
          "token.actions.githubusercontent.com:sub": "repo:NobleChicken97/self-healing-distributed-cache:*"
        }
      }
    }
  ]
}
```

### Create the role:

```bash
# Create role
aws iam create-role \
  --role-name github-actions-ecr-role \
  --assume-role-policy-document file://github-oidc-trust.json

# Attach ECR push policy
aws iam attach-role-policy \
  --role-name github-actions-ecr-role \
  --policy-arn arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryFullAccess

# Get the role ARN
aws iam get-role --role-name github-actions-ecr-role --query Role.Arn --output text
```

---

## Step 4: Configure GitHub Secrets

Go to **GitHub Repository → Settings → Secrets and variables → Actions**

Add these secrets:

| Secret Name | Value | How to Get It |
|-------------|-------|---------------|
| `AWS_ROLE_ARN` | `arn:aws:iam::123456789:role/github-actions-ecr-role` | From Step 3 |
| `LIGHTSAIL_NODE1_IP` | `13.126.24.246` | Terraform output |
| `LIGHTSAIL_NODE2_IP` | `13.127.78.189` | Terraform output |
| `LIGHTSAIL_NODE3_IP` | `15.252.208.189` | Terraform output |
| `LIGHTSAIL_SSH_KEY` | (private key content) | From `~/.ssh/cache-cluster-key` |
| `ECR_REGISTRY` | `123456789.dkr.ecr.ap-south-1.amazonaws.com` | From ECR console |

### To get the SSH key content:

```bash
# On macOS/Linux:
cat ~/.ssh/cache-cluster-key

# On Windows (PowerShell):
Get-Content $env:USERPROFILE\.ssh\cache-cluster-key
```

Copy the entire content (including `-----BEGIN RSA PRIVATE KEY-----` and `-----END RSA PRIVATE KEY-----`)

---

## Step 5: Install AWS CLI on Lightsail Nodes

Each Lightsail node needs AWS CLI to pull from ECR.

SSH into each node and run:

```bash
# Install AWS CLI
curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
unzip awscliv2.zip
sudo ./aws/install

# Verify
aws --version

# Configure region (no credentials needed - uses instance role or IAM user)
aws configure set region ap-south-1
```

---

## Step 6: Push to Trigger Pipeline

```bash
# Make a change
echo "# $(date)" >> README.md
git add .
git commit -m "Trigger CD pipeline"
git push origin main
```

The pipeline will:
1. Run tests
2. Build Docker image
3. Push to ECR
4. SSH into each Lightsail node
5. Pull new image
6. Restart containers

---

## Manual Deploy (Without GitHub Actions)

If you want to deploy manually:

```bash
# Build and push to ECR
cd deploy/
aws ecr get-login-password --region ap-south-1 | docker login --username AWS --password-stdin $(terraform output -raw ecr_repository_url)
docker build -t cache-server ..
docker tag cache-server:latest $(terraform output -raw ecr_repository_url):latest
docker push $(terraform output -raw ecr_repository_url):latest

# Deploy to nodes
./deploy.sh latest
```

---

## Monitoring Deployments

### Check GitHub Actions:
- Go to **GitHub Repository → Actions** tab

### Check ECR images:
```bash
aws ecr describe-images --repository-name self-healing-cache --region ap-south-1
```

### Check node status:
```bash
curl http://13.126.24.246:8080/ring/info
curl http://13.127.78.189:8080/ring/info
curl http://15.252.208.189:8080/ring/info
```

---

## Troubleshooting

### GitHub Actions can't push to ECR
- Verify OIDC provider is created
- Check IAM role trust policy matches your repo
- Verify `AWS_ROLE_ARN` secret is correct

### Lightsail nodes can't pull from ECR
- Ensure AWS CLI is installed on nodes
- Check node has IAM permissions or configure AWS credentials
- Verify ECR repository exists

### SSH connection fails
- Verify SSH key is correct in GitHub secrets
- Check Lightsail firewall allows port 22
- Ensure instances are running

---

## Cost Breakdown

| Service | Cost |
|---------|------|
| 3× Lightsail Nano | $15/month |
| ECR Storage | ~$0.10/month (first 500MB free) |
| ECR Data Transfer | ~$0.10/month |
| GitHub Actions | Free (2000 minutes/month) |
| **Total** | **~$15.20/month** |
