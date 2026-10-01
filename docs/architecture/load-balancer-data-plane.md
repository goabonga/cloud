# Load balancer data plane (infra-lb)

`infra-lb` is the data plane of the layer-7 load balancers: an HTTP(S)
reverse proxy, a TLS passthrough router and a TCP splicer in one process,
driven by a JSON configuration file it reloads on change, and reporting the
state of its listeners and targets in a JSON status file.

`infra-lb` is built (`make build-lb`) and tested, but nothing runs it yet: it
is neither packaged nor started by the agent, and no resource of the API
renders its configuration so far.

```bash
infra-lb -config /etc/infra/lb.json -status /run/infra/lb-status.json
```

| Flag | Environment | Default | |
| --- | --- | --- | --- |
| `-config` | `GOA_LB_CONFIG` | `/etc/infra/lb.json` | configuration file |
| `-status` | `GOA_LB_STATUS` | none | status file, rewritten every second |

## Configuration

```json
{
  "listeners": [
    {
      "name": "https",
      "address": "203.0.113.10",
      "port": 443,
      "protocol": "https",
      "tlsMode": "reencrypt",
      "certificates": [{ "certPem": "...", "keyPem": "..." }],
      "defaultTargetGroup": "web",
      "rules": [
        { "host": "api.demo.test", "targetGroup": "api" },
        { "pathPrefix": "/static", "targetGroup": "static" }
      ]
    }
  ],
  "targetGroups": [
    {
      "name": "web",
      "protocol": "https",
      "backendCaPem": "...",
      "serverName": "",
      "healthCheck": {
        "protocol": "https", "path": "/healthz", "port": 0,
        "intervalSeconds": 5, "timeoutSeconds": 2,
        "healthyThreshold": 2, "unhealthyThreshold": 3
      },
      "targets": [{ "id": "web-1", "address": "10.20.1.10", "port": 443, "weight": 1 }]
    }
  ]
}
```

A listener's protocol and TLS mode decide what it does and which target
groups it may send to:

| Listener | TLS mode | Behaviour | Target groups |
| --- | --- | --- | --- |
| `http` | - | reverse proxy | `http` |
| `https` | `terminate` (default) | TLS ends here, HTTP to the backends | `http` |
| `https` | `reencrypt` | TLS ends here, TLS again to the backends, verified against `backendCaPem` | `https` |
| `tls` | `passthrough` (only mode) | routes the connection by its SNI, never decrypts it | `tcp`, `https` |
| `tcp` | - | splices the connection to the default group | `tcp` |

- **Certificates** (https only): a PEM chain, leaf first, and its key. The one
  whose leaf names the client's SNI is served - exactly, or under a
  `*.example.com` name - and the first otherwise.
- **Rules**: a rule matches its `host` (case-insensitive, port stripped,
  `*.suffix` for any name below) and its `pathPrefix`; the longest matching
  prefix wins, a rule naming the host winning a tie, and a request no rule
  matches goes to `defaultTargetGroup`. A `tls` listener matches the host
  against the SNI and has no paths; a `tcp` listener has no rules.
- **Backends over TLS**: verified against `backendCaPem`, or the system roots
  without it, and against `serverName`, or the request's host without it. A
  health check, which has no request host, verifies the chain only.
- **Forwarded headers**: the layer-7 listeners set `X-Forwarded-For`,
  `X-Forwarded-Proto` and `X-Forwarded-Host` and keep the client's `Host`.

Defaults: a target's `weight` is 1 and its `id` is `address:port`; a health
check uses the group's protocol, path `/`, every 5 s with a 2 s timeout, and
thresholds of 2 and 3. Unknown fields are rejected.

## Balancing and health

- **Weighted round robin** (smooth: weights interleave) over the eligible
  targets. A target starts `unknown`, which counts as eligible.
- **Active checks** probe every target: a TCP connect, or a GET expecting a 2xx
  or 3xx. `healthyThreshold` consecutive successes make it `healthy`,
  `unhealthyThreshold` consecutive failures `unhealthy`.
- **Passive checks**: `unhealthyThreshold` consecutive failed connections or
  5xx answers eject a target as well, until active checks bring it back.
- **Fail open**: with no eligible target left, traffic goes to every target
  rather than nowhere.
- **Retries**: a request goes to a second target when its connection to the
  first could not be established (any method, the body not being sent yet), or
  when it is a `GET`, `HEAD` or `OPTIONS` whose body was not consumed. A `tcp`
  or `tls` connection tries a second target when the first cannot be reached.

## Reloads

The configuration file is checked every second, by modification time and size,
and reloaded at once on `SIGHUP`. A configuration that does not load is
reported in the status and the running one stays: a bad push never takes the
data plane down. A configuration that loads is applied as a diff:

- a listener whose address, port, protocol and TLS mode are unchanged keeps
  its socket, and takes its new rules and certificates atomically;
- a removed or rebound listener stops accepting at once and drains its
  in-flight requests and connections for up to 10 seconds;
- a target keeping its group, address and port keeps its health.

Sockets are bound with `SO_REUSEPORT`, so a restarted `infra-lb` can bind next
to the one draining, and `IP_FREEBIND`, so a listener can bind an address not
assigned yet. A listener that cannot bind is retried every second. `SIGTERM`
and `SIGINT` stop accepting and drain for up to 10 seconds.

## Status

Rewritten every second and after every reload, atomically (a temporary file
renamed over it, mode 0644):

```json
{
  "configGeneration": 2,
  "lastError": "",
  "listeners": [
    { "name": "https", "address": "203.0.113.10", "port": 443, "bound": true }
  ],
  "targetGroups": [
    {
      "name": "web",
      "targets": [
        { "id": "web-1", "address": "10.20.1.10", "port": 443, "health": "healthy" }
      ]
    }
  ]
}
```

`configGeneration` counts the configurations applied since start; `lastError`
is why the latest one was rejected; a listener's `error` is why it could not
bind, and a target's `lastError` its latest failed check or connection.
