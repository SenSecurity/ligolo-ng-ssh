# Ligolo-ng SSH

[![Release](https://img.shields.io/github/v/release/SenSecurity/ligolo-ng-ssh)](https://github.com/SenSecurity/ligolo-ng-ssh/releases)
[![Downloads](https://img.shields.io/github/downloads/SenSecurity/ligolo-ng-ssh/total)](https://github.com/SenSecurity/ligolo-ng-ssh/releases)
[![GPLv3](https://img.shields.io/badge/License-GPLv3-brightgreen.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Sponsor upstream](https://img.shields.io/github/sponsors/nicocha30?label=Sponsor%20upstream)](https://github.com/sponsors/nicocha30)

Ligolo-ng with a real SSH agent-to-proxy transport. It is designed for
authorized assessment environments where outbound SSH works but TLS,
WebSockets, or arbitrary TCP transports do not.

> [!IMPORTANT]
> This repository is an independent fork of
> [nicocha30/ligolo-ng](https://github.com/nicocha30/ligolo-ng).
> Ligolo-ng was created by
> [Nicolas Chatelain (@nicocha30)](https://github.com/nicocha30), who deserves
> full credit for the original project, core tunneling engine, and ongoing
> upstream work. This is not an official Ligolo-ng release and is not endorsed
> by the upstream maintainers.

## Download

Download the bundle for the proxy and target architecture from
[GitHub Releases](https://github.com/SenSecurity/ligolo-ng-ssh/releases/latest):

- `ligolo-ng-ssh-windows-amd64.zip`
- `ligolo-ng-ssh-windows-arm64.zip`
- `ligolo-ng-ssh-linux-amd64.tar.gz`
- `ligolo-ng-ssh-linux-arm64.tar.gz`

Each bundle contains:

- `proxy`
- `agent`
- `agent-garble`
- `README.md`

Individual Windows executables and `SHA256SUMS.txt` are also attached to the
release.

## Copy/paste SSH recipes

Pick one mode. In that mode:

1. Run `Setup` and `Start proxy` for your proxy OS.
2. Run the target block for your target OS.

Proxy and target OS can differ.

Replace:

- `PROXY_IP`
- `SHA256:PROXY_FINGERPRINT`

SSH proxy always needs a host key. "No key" below means no agent key on target.

Setup assumes the named key files do not exist. If they exist, stop and reuse
the complete existing key set or use a fresh directory. Do not overwrite keys
used by a running proxy.

Modes 2 and 3 accept unauthenticated agents. `0.0.0.0:22` exposes SSH on every
interface. Restrict TCP/22 to authorized source IPs with a firewall. Stop proxy
when assessment ends.

<details open>
<summary><strong>Mode 1: agent key + proxy fingerprint</strong></summary>

Target files: agent binary + private `agent` key.

#### Windows proxy

Setup:

```powershell
New-Item -ItemType Directory -Force .\keys
ssh-keygen -t ed25519 -f .\keys\proxy-host
ssh-keygen -t ed25519 -f .\keys\agent
Copy-Item .\keys\agent.pub .\keys\authorized_keys
ssh-keygen -lf .\keys\proxy-host.pub -E sha256
```

For each `ssh-keygen` command, press Enter twice for empty passphrase. Copy
`agent.exe` and `.\keys\agent` to target. Name private key `agent`.

Start proxy:

```powershell
.\proxy.exe `
  -laddr ssh://0.0.0.0:22 `
  -ssh-user ligolo `
  -ssh-host-key .\keys\proxy-host `
  -ssh-authorized-keys .\keys\authorized_keys
```

Windows target:

```powershell
.\agent.exe `
  -connect ssh://PROXY_IP:22 `
  -ssh-user ligolo `
  -ssh-key .\agent `
  -ssh-host-key-fingerprint "SHA256:PROXY_FINGERPRINT" `
  -retry `
  -reconnect
```

#### Linux proxy

Setup:

```bash
mkdir -p ./keys
ssh-keygen -t ed25519 -f ./keys/proxy-host -N ''
ssh-keygen -t ed25519 -f ./keys/agent -N ''
cp ./keys/agent.pub ./keys/authorized_keys
ssh-keygen -lf ./keys/proxy-host.pub -E sha256
```

Copy `agent` binary and `./keys/agent` private key to target. Name private key
`agent-key`.

Start proxy:

```bash
sudo ./proxy \
  -laddr ssh://0.0.0.0:22 \
  -ssh-user ligolo \
  -ssh-host-key ./keys/proxy-host \
  -ssh-authorized-keys ./keys/authorized_keys
```

Linux target:

```bash
./agent \
  -connect ssh://PROXY_IP:22 \
  -ssh-user ligolo \
  -ssh-key ./agent-key \
  -ssh-host-key-fingerprint 'SHA256:PROXY_FINGERPRINT' \
  -retry \
  -reconnect
```

</details>

<details>
<summary><strong>Mode 2: no agent key + proxy fingerprint</strong></summary>

Target file: agent binary only. Proxy identity still verified.

#### Windows proxy

Setup:

```powershell
New-Item -ItemType Directory -Force .\keys
ssh-keygen -t ed25519 -f .\keys\proxy-host
ssh-keygen -lf .\keys\proxy-host.pub -E sha256
```

Press Enter twice for empty passphrase.

Start proxy:

```powershell
.\proxy.exe `
  -laddr ssh://0.0.0.0:22 `
  -ssh-user ligolo `
  -ssh-host-key .\keys\proxy-host `
  -ssh-insecure-allow-no-auth
```

Windows target:

```powershell
.\agent.exe `
  -connect ssh://PROXY_IP:22 `
  -ssh-user ligolo `
  -ssh-insecure-no-auth `
  -ssh-host-key-fingerprint "SHA256:PROXY_FINGERPRINT" `
  -retry `
  -reconnect
```

#### Linux proxy

Setup:

```bash
mkdir -p ./keys
ssh-keygen -t ed25519 -f ./keys/proxy-host -N ''
ssh-keygen -lf ./keys/proxy-host.pub -E sha256
```

Start proxy:

```bash
sudo ./proxy \
  -laddr ssh://0.0.0.0:22 \
  -ssh-user ligolo \
  -ssh-host-key ./keys/proxy-host \
  -ssh-insecure-allow-no-auth
```

Linux target:

```bash
./agent \
  -connect ssh://PROXY_IP:22 \
  -ssh-user ligolo \
  -ssh-insecure-no-auth \
  -ssh-host-key-fingerprint 'SHA256:PROXY_FINGERPRINT' \
  -retry \
  -reconnect
```

</details>

<details>
<summary><strong>Mode 3: binary only + no proxy pinning</strong></summary>

Target file: agent binary only. Fastest mode. No agent or proxy authentication.

#### Windows proxy

Setup:

```powershell
New-Item -ItemType Directory -Force .\keys
ssh-keygen -t ed25519 -f .\keys\proxy-host
```

Press Enter twice for empty passphrase.

Start proxy:

```powershell
.\proxy.exe `
  -laddr ssh://0.0.0.0:22 `
  -ssh-user ligolo `
  -ssh-host-key .\keys\proxy-host `
  -ssh-insecure-allow-no-auth
```

Windows target:

```powershell
.\agent.exe `
  -connect ssh://PROXY_IP:22 `
  -ssh-user ligolo `
  -ssh-insecure-no-auth `
  -ssh-insecure-ignore-host-key `
  -retry `
  -reconnect
```

#### Linux proxy

Setup:

```bash
mkdir -p ./keys
ssh-keygen -t ed25519 -f ./keys/proxy-host -N ''
```

Start proxy:

```bash
sudo ./proxy \
  -laddr ssh://0.0.0.0:22 \
  -ssh-user ligolo \
  -ssh-host-key ./keys/proxy-host \
  -ssh-insecure-allow-no-auth
```

Linux target:

```bash
./agent \
  -connect ssh://PROXY_IP:22 \
  -ssh-user ligolo \
  -ssh-insecure-no-auth \
  -ssh-insecure-ignore-host-key \
  -retry \
  -reconnect
```

</details>

<details>
<summary>Detailed SSH setup and security notes</summary>

## Choose an SSH mode

| Mode | Files required on target | Agent authenticated | Proxy verified |
| --- | --- | --- | --- |
| Agent key + fingerprint | Agent binary and private key | Yes | Yes |
| No agent key + fingerprint | Agent binary only | No | Yes |
| Zero-file / no pinning | Agent binary only | No | No |

The first mode is recommended. The third mode is intentionally insecure and
should only be enabled for a short, controlled assessment window.

The proxy always requires an SSH host key. This key encrypts the SSH transport
and identifies the proxy. The no-key modes remove only the target-side agent
key.

## Recommended: agent key and proxy fingerprint

### 1. Generate keys on the proxy

PowerShell:

```powershell
New-Item -ItemType Directory -Force .\keys
ssh-keygen -t ed25519 -f .\keys\proxy-host
ssh-keygen -t ed25519 -f .\keys\agent
Copy-Item .\keys\agent.pub .\keys\authorized_keys
ssh-keygen -lf .\keys\proxy-host.pub -E sha256
```

Press Enter twice for an empty passphrase, or use a passphrase together with
the relevant `-ssh-*-passphrase-env` flag.

Linux:

```bash
mkdir -p ./keys
ssh-keygen -t ed25519 -f ./keys/proxy-host -N ''
ssh-keygen -t ed25519 -f ./keys/agent -N ''
cp ./keys/agent.pub ./keys/authorized_keys
ssh-keygen -lf ./keys/proxy-host.pub -E sha256
```

Keep the `SHA256:...` fingerprint printed by the last command.

### 2. Start the proxy

Windows:

```powershell
.\proxy.exe `
  -laddr ssh://0.0.0.0:22 `
  -ssh-user ligolo `
  -ssh-host-key .\keys\proxy-host `
  -ssh-authorized-keys .\keys\authorized_keys
```

Linux:

```bash
sudo ./proxy \
  -laddr ssh://0.0.0.0:22 \
  -ssh-user ligolo \
  -ssh-host-key ./keys/proxy-host \
  -ssh-authorized-keys ./keys/authorized_keys
```

### 3. Copy the agent and key to the target

Copy these two files:

- `agent.exe` and the private key `agent` on Windows
- `agent` and the private key `agent` on Linux

The Garble build accepts the same flags and can replace the normal agent.

### 4. Start the agent

Windows:

```powershell
.\agent.exe `
  -connect ssh://PROXY_IP:22 `
  -ssh-user ligolo `
  -ssh-key .\agent `
  -ssh-host-key-fingerprint "SHA256:REPLACE_WITH_PROXY_FINGERPRINT" `
  -retry `
  -reconnect
```

Linux:

```bash
./agent \
  -connect ssh://PROXY_IP:22 \
  -ssh-user ligolo \
  -ssh-key ./agent \
  -ssh-host-key-fingerprint 'SHA256:REPLACE_WITH_PROXY_FINGERPRINT' \
  -retry \
  -reconnect
```

## No agent key: send only the binary

No-key mode requires an explicit flag on both sides. The flag names are
different:

- Proxy: `-ssh-insecure-allow-no-auth`
- Normal or ShadowPort agent: `-ssh-insecure-no-auth`

The proxy command below is required for both the fingerprint-pinned and
zero-file agent commands. A no-key agent cannot connect to a proxy started
without `-ssh-insecure-allow-no-auth`.

Generate only the proxy host key:

PowerShell:

```powershell
New-Item -ItemType Directory -Force .\keys
ssh-keygen -t ed25519 -f .\keys\proxy-host
ssh-keygen -lf .\keys\proxy-host.pub -E sha256
```

Linux:

```bash
mkdir -p ./keys
ssh-keygen -t ed25519 -f ./keys/proxy-host -N ''
ssh-keygen -lf ./keys/proxy-host.pub -E sha256
```

### Start the no-auth proxy

Windows:

```powershell
.\proxy.exe `
  -laddr ssh://0.0.0.0:22 `
  -ssh-user ligolo `
  -ssh-host-key .\keys\proxy-host `
  -ssh-insecure-allow-no-auth
```

Linux:

```bash
sudo ./proxy \
  -laddr ssh://0.0.0.0:22 \
  -ssh-user ligolo \
  -ssh-host-key ./keys/proxy-host \
  -ssh-insecure-allow-no-auth
```

### Safer no-key agent: verify the proxy fingerprint

Windows:

```powershell
.\agent.exe `
  -connect ssh://PROXY_IP:22 `
  -ssh-user ligolo `
  -ssh-insecure-no-auth `
  -ssh-host-key-fingerprint "SHA256:REPLACE_WITH_PROXY_FINGERPRINT" `
  -retry `
  -reconnect
```

Linux:

```bash
./agent \
  -connect ssh://PROXY_IP:22 \
  -ssh-user ligolo \
  -ssh-insecure-no-auth \
  -ssh-host-key-fingerprint 'SHA256:REPLACE_WITH_PROXY_FINGERPRINT' \
  -retry \
  -reconnect
```

### Zero-file agent: binary only, no proxy pinning

Windows:

```powershell
.\agent.exe `
  -connect ssh://PROXY_IP:22 `
  -ssh-user ligolo `
  -ssh-insecure-no-auth `
  -ssh-insecure-ignore-host-key `
  -retry `
  -reconnect
```

Linux:

```bash
./agent \
  -connect ssh://PROXY_IP:22 \
  -ssh-user ligolo \
  -ssh-insecure-no-auth \
  -ssh-insecure-ignore-host-key \
  -retry \
  -reconnect
```

This still runs a real encrypted SSH handshake and the Ligolo protocol inside
the custom `ligolo` SSH channel. It does not authenticate the agent. With
`-ssh-insecure-ignore-host-key`, it also does not authenticate the proxy.
Anyone who can reach the no-auth proxy can attempt to connect, and an active
man-in-the-middle can impersonate the proxy. Restrict the listener with
firewall rules and disable no-auth mode after the assessment.

Both sides must opt in. A no-auth agent is rejected by a normal proxy.

</details>

## Start the tunnel

After the agent connects, use the interactive proxy:

```text
session
autoroute
```

Select the agent, desired routes, interface name, and confirm tunnel startup.
`autoroute` creates the interface, adds routes, and starts the tunnel.

Manual equivalent:

```text
interface_create --name ligolo
route_add --name ligolo --route 10.10.0.0/24
session
tunnel_start --tun ligolo
```

Replace `10.10.0.0/24` with the network reachable from the agent.

## Web UI

SSH agents work in the Web UI exactly like the original Ligolo-ng agents.
Create `ligolo-ng.yaml`:

```yaml
web:
  enabled: true
  enableui: true
  listen: 127.0.0.1:8080
  users:
    admin: CHANGE_THIS_PASSWORD
```

Start the proxy with `-daemon`, the Web config, and one of the SSH
authentication configurations shown above. Example without an agent key:

Windows:

```powershell
.\proxy.exe `
  -daemon `
  -config .\ligolo-ng.yaml `
  -laddr ssh://0.0.0.0:22 `
  -ssh-user ligolo `
  -ssh-host-key .\keys\proxy-host `
  -ssh-insecure-allow-no-auth
```

Linux:

```bash
sudo ./proxy \
  -daemon \
  -config ./ligolo-ng.yaml \
  -laddr ssh://0.0.0.0:22 \
  -ssh-user ligolo \
  -ssh-host-key ./keys/proxy-host \
  -ssh-insecure-allow-no-auth
```

Open `http://127.0.0.1:8080`. Keep the UI/API on localhost or behind a trusted
reverse proxy.

## SSH flags

Proxy:

```text
-ssh-user
-ssh-host-key
-ssh-host-key-passphrase-env
-ssh-authorized-keys
-ssh-insecure-allow-no-auth
-ssh-handshake-timeout
```

Agent:

```text
-ssh-user
-ssh-key
-ssh-key-passphrase-env
-ssh-known-hosts
-ssh-host-key-fingerprint
-ssh-insecure-ignore-host-key
-ssh-insecure-no-auth
-ssh-connect-timeout
-ssh-keepalive
```

Port 22 may require Administrator/root and may conflict with OpenSSH. Any
allowed port can be used, for example `ssh://0.0.0.0:2222`.

## Features preserved from Ligolo-ng

The SSH transport carries the existing Ligolo-ng protocol. TUN routing,
TCP/UDP/ICMP, reverse listeners, multiple agents, reconnect recovery, daemon
mode, API, Web UI, and the original TLS/WebSocket transports remain available.

Implementation details and security boundaries are documented in
[`doc/ssh-transport.md`](doc/ssh-transport.md). Runtime evidence is available
in [`artifacts/TEST-REPORT.md`](artifacts/TEST-REPORT.md).

## Credits and license

- **Original Ligolo-ng project and core implementation:** [Nicolas Chatelain
  (@nicocha30)](https://github.com/nicocha30/ligolo-ng)
- **Original Ligolo-ng Web UI:** [Jeremie
  Bedjai](https://github.com/jeremiebedjai)
- **SSH transport fork changes:** [SenSecurity](https://github.com/SenSecurity)

This fork remains licensed under the
[GNU General Public License v3.0](LICENSE).

Please report upstream Ligolo-ng issues to the
[original project](https://github.com/nicocha30/ligolo-ng). Report issues
specific to this SSH transport in this repository.
