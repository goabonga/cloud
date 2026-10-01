# infra_function

Manages a FaaS function: the compute shape run for each invocation, plus a
warm-pool policy. Realized as ordinary compute instances that a controller
creates and retires to the policy. Invoking a function is not implemented
yet; see [FaaS functions](../../architecture/faas.md).

## Example

```hcl
resource "infra_function" "resize_image" {
  name      = "resize-image"
  subnet_id = infra_subnet.app.id
  image     = "registry.example.com/resize-image:1.4"

  env = {
    OUTPUT_BUCKET = "resized"
  }

  warm_pool = {
    min_warm         = 1
    max_warm         = 5
    idle_ttl_seconds = 300
  }
}
```

An always-warm function, never scaling to zero:

```hcl
resource "infra_function" "latency_sensitive" {
  name      = "latency-sensitive"
  subnet_id = infra_subnet.app.id
  image     = "registry.example.com/latency-sensitive:2.0"

  warm_pool = {
    min_warm         = 2
    allow_cold_start = false
  }
}
```

## Argument reference

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `subnet_id` | string | yes | Subnet each instance attaches to. |
| `image` | string | yes | OCI image reference run on invocation. |
| `name` | string | no | Display name. |
| `security_group_id` | string | no | Security group applied to each instance. |
| `node_pool_id` | string | no | Node pool to schedule instances onto; empty schedules anywhere. |
| `cpu` | number | no | CPU cores per instance. |
| `memory_mb` | number | no | Memory in MB per instance. |
| `pids_max` | number | no | Maximum processes per instance. |
| `command` | string | no | Entrypoint command. |
| `env` | map of string | no | Environment variables. |
| `warm_pool` | object | no | How many instances to keep pre-started and what to do when none are available. See below. |

### `warm_pool`

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `min_warm` | number | no | Instances kept running regardless of idle time; 0 by default. |
| `max_warm` | number | no | Cap on concurrent warm instances; 0, the default, is unbounded. |
| `idle_ttl_seconds` | number | no | Seconds an instance above `min_warm` may sit idle before eviction; 0 evicts as soon as it is idle. |
| `allow_cold_start` | bool | no | Permit creating a fresh instance on invoke when none are warm. Reserved: the invoke path does not exist yet. |

## Attribute reference

In addition to the arguments above, the following are exported:

| Name | Description |
| --- | --- |
| `id` | System-assigned unique identifier. |
| `phase` | Lifecycle phase reported by the control plane. |
| `warm_count` | Number of instances currently warm and backed by a ready compute. |

## Import

```shell
terraform import infra_function.resize_image function-abc123
```
