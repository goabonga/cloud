# Installation

## From a release

Each component is published to GitHub Releases as a cross-compiled binary with a
checksum file, tagged `{component}-v{version}` (for example
`infra-api-v0.1.0`). Download the binary for your platform, verify the checksum
and place it on your `PATH`:

```bash
sha256sum -c infra-api_0.1.0_checksums.txt
install -m 0755 infra-api_0.1.0_linux_amd64 /usr/local/bin/infra-api
```

## From source

```bash
make build
sudo install -m 0755 build/infra-api /usr/local/bin/infra-api
```

## Service units and packages

systemd units and `.deb` packaging land with the operations milestone; see the
[deployment guide](../operations/deploy.md).
