# ECR Repository for cache server Docker images

resource "aws_ecr_repository" "cache" {
  name                 = "self-healing-cache"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = {
    Project = "self-healing-cache"
  }
}

# Lifecycle policy: keep only last 10 images
resource "aws_ecr_lifecycle_policy" "cache" {
  repository = aws_ecr_repository.cache.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Keep last 10 images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = {
          type = "expire"
        }
      }
    ]
  })
}

output "ecr_repository_url" {
  description = "ECR repository URL for docker push"
  value       = aws_ecr_repository.cache.repository_url
}

output "ecr_login_command" {
  description = "Command to login to ECR"
  value       = "aws ecr get-login-password --region ${var.aws_region} | docker login --username AWS --password-stdin ${aws_ecr_repository.cache.registry_id}.dkr.ecr.${var.aws_region}.amazonaws.com"
}
