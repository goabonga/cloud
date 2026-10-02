# infra_vpc

Manages a virtual private cloud: an isolated Linux bridge fabric.

## Example

```hcl
resource "infra_vpc" "prod" {
  name = "prod"
  cidr = "10.0.0.0/16"
}
```

## Argument reference

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `cidr` | string | yes | Address range in CIDR notation, e.g. `10.0.0.0/16`. |
| `name` | string | no | Display name. |

## Attribute reference

In addition to the arguments above, the following are exported:

| Name | Description |
| --- | --- |
| `id` | System-assigned unique identifier. |
| `bridge_name` | Linux bridge backing the VPC, set by the agent. |
| `phase` | Lifecycle phase reported by the control plane. |

## Import

```shell
terraform import infra_vpc.prod vpc-abc123
```
