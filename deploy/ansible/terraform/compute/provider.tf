terraform {
  required_providers {
    infra = {
      source = "goabonga/infra"
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
  # Run `export GOA_API_TOKEN="$(../get-token.sh)"` first (see ../README.md).
}
