terraform {
  required_providers {
    infra = {
      source = "goabonga/infra"
    }
    # Generates the k8s micro-VM demo's SSH key pair (main.tf) - a public
    # registry provider, unlike infra, so it needs a real `terraform init`
    # alongside the dev_overrides used for infra (see README.md).
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }
}

variable "endpoint" {
  description = "infra-api base URL (the control host)."
  type        = string
  default     = "http://192.168.122.10:8080"
}

provider "infra" {
  endpoint = var.endpoint
  # The bearer token is read from GOA_API_TOKEN: a JWT issued by infra-idp.
  # Run `export GOA_API_TOKEN="$(./get-token.sh)"` first (see README.md).
}
