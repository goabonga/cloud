# infra_function

Manages a FaaS function: the compute shape run for each invocation, plus a
warm-pool policy. Realized as ordinary compute instances that a controller
creates and retires to the policy, invoked synchronously over HTTP; see
[FaaS functions](../../architecture/faas.md).

## Example

```hcl
resource "infra_function" "resize_image" {
  name      = "resize-image"
  subnet_id = infra_subnet.app.id
  image     = "registry.example.com/resize-image:1.4"
  port      = 8080

  env = {
    OUTPUT_BUCKET = "resized"
  }

  warm_pool = {
    min_warm         = 1
    max_warm         = 5
    idle_ttl_seconds = 300
    allow_cold_start = true
  }
}
```

Invoking it (outside Terraform, against the control-plane API):

```shell
curl -X POST "$INFRA_API/api/v1/function/${resize_image_id}/invoke" \
  --data-binary @photo.jpg
```

An always-warm function, never scaling to zero and never cold-starting:

```hcl
resource "infra_function" "latency_sensitive" {
  name      = "latency-sensitive"
  subnet_id = infra_subnet.app.id
  image     = "registry.example.com/latency-sensitive:2.0"
  port      = 8080

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
| `cpu` | number | no | CPU cores per instance; defaults to 1, allowed range 0.01–64. |
| `memory_mb` | number | no | Memory per instance; defaults to 256 MiB, maximum 262144 MiB. |
| `pids_max` | number | no | Maximum processes per instance; defaults to 256, maximum 4096. |
| `command` | string | no | Entrypoint command. |
| `env` | map of string | no | Environment variables. |
| `port` | number | yes | Port the runtime listens on inside the instance; invoke requests are forwarded to it. |
| `warm_pool` | object | no | How many instances to keep pre-started and what to do when none are available. See below. |

### `warm_pool`

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `min_warm` | number | no | Instances kept running regardless of idle time; 0 by default, maximum 256. |
| `max_warm` | number | no | Cap on warm instances; defaults to max(32, min_warm), maximum 256. Omit this attribute to use its default. |
| `idle_ttl_seconds` | number | no | Seconds an instance above `min_warm` may sit idle before eviction; 0 evicts as soon as it is idle. |
| `allow_cold_start` | bool | no | Permit creating a fresh instance on invoke when none are warm, bypassing `min_warm`/`max_warm`. |

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


Explicit zero `cpu`, `memory_mb`, `pids_max` or `max_warm` values are rejected by
Terraform validation. Remove these attributes to use finite platform defaults.
Existing computes with zero limits receive the same defaults on reconciliation;
this can restart a workload when its effective runtime configuration changes.
These are per-workload admission limits, not aggregate per-project quotas.
