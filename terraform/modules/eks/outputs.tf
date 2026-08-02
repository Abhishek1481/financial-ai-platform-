output "cluster_name" {
  value = aws_eks_cluster.this.name
}

output "cluster_endpoint" {
  value = aws_eks_cluster.this.endpoint
}

output "cluster_certificate_authority_data" {
  value = aws_eks_cluster.this.certificate_authority[0].data
}

output "gateway_go_irsa_role_arn" {
  description = "Annotate the gateway-go ServiceAccount with eks.amazonaws.com/role-arn set to this (see k8s/base/gateway-go-deployment.yaml — no ServiceAccount is defined there yet; add one with this annotation to actually use IRSA rather than static credentials)."
  value       = aws_iam_role.gateway_go_irsa.arn
}

output "ml_service_irsa_role_arn" {
  description = "Same as gateway_go_irsa_role_arn, for ml-service's ServiceAccount."
  value       = aws_iam_role.ml_service_irsa.arn
}

output "configure_kubectl" {
  value = "aws eks update-kubeconfig --name ${aws_eks_cluster.this.name} --region <your-region>"
}
