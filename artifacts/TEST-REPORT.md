# Ligolo-ng SSH fork - verification report

- Date: 2026-07-29
- Upstream: `nicocha30/ligolo-ng` at `60fb207404d35a0674874220a5b93de1d2bdcfb0`
- Web UI fork: `SenSecurity/ligolo-ng-web` at `936dbda884a0a9e975f4c82f993f57a5e21f4afa`
- Build source: `341e1e7207565019ddfa2a59f9639496e4a91de1`
- Build version: `ssh-341e1e720756`

## Scope

The fork adds an embedded SSH server to the proxy and an outbound SSH client
to the agent. Ligolo's existing yamux and protocol layers run inside a custom
SSH channel named `ligolo`. Existing TUN, listener, reconnection, daemon, API,
Web UI, autoroute, and legacy TLS/WebSocket paths remain in the upstream core.

SSH authentication is public-key-only by default. An explicit no-auth mode
allows the victim to receive only the agent binary. Both proxy and agent must
opt in. Host-key pinning or `known_hosts` remains the default; a separate
dangerous flag permits zero-file operation without proxy verification.
Encrypted private keys, timeouts, and keepalives are supported.

## Build environment

- Windows 10 Pro 22H2, build 19045, AMD64
- WSL Ubuntu 22.04.2 LTS, AMD64
- Go 1.26.5, isolated under `.tools`
- Garble v0.17.0 with `-literals -tiny`
- Node.js 25.8.0 and npm 11.11.0
- QEMU user static 6.2.0 for Linux ARM64 smoke execution
- `CGO_ENABLED=0`, `-trimpath`, stripped release binaries

The build manifest records the exact main commit, Web UI commit, dirty-state
flags, Web UI lockfile hash, build time, target tuples, and per-target random
Garble seeds.

## Automated verification

Passed:

- `go test ./...`
- `go vet ./...`
- `go test -race ./pkg/sshtransport ./pkg/protocol`
- `git diff --check`
- Real SSH banner test
- SSH bidirectional stream test
- Authorized key and username rejection tests
- No-auth requires explicit opt-in on both sides
- No-auth pinned-fingerprint and zero-file round-trip tests
- No-auth rejects an incorrect username and key/no-auth flag combinations
- Fingerprint and `known_hosts` verification tests
- Explicit MsgPack wire-name compatibility test for Garble builds

The final `release` directory contains the original 12 binaries plus two
Windows AMD64 ShadowPort agents, four platform directories, one root
`README.md`, and the WinDivert license. All 16 SHA256 hashes were recomputed
and matched `artifacts/SHA256SUMS`.

Architecture inspection:

- Windows AMD64: five PE32+ x86-64 console executables
- Windows ARM64: three PE32+ AArch64 console executables
- Linux AMD64: three statically linked ELF x86-64 executables
- Linux ARM64: three statically linked ELF AArch64 executables

## Final release runtime verification

All checks below used the final binaries identified by `SHA256SUMS`.

### Windows agent to WSL Linux proxy

- Public-key-authenticated SSH connection and agent registration: passed
- TUN creation and route installation: passed
- TCP exact echo payload `KEY-NORMAL-TCP`: passed
- UDP exact echo payload `KEY-NORMAL-UDP`: passed
- ICMP: 1 transmitted, 1 received, 0% loss
- Replaced normal agent with Garble agent using the same identity: session and
  TUN automatically recovered
- TCP exact echo payload `KEY-GARBLE-TCP`: passed
- UDP exact echo payload `KEY-GARBLE-UDP`: passed

### No agent key / zero-file mode

Final release binaries were run without `-ssh-key`, `authorized_keys`, a
fingerprint, or a `known_hosts` file on the agent:

```text
agent -ssh-insecure-no-auth -ssh-insecure-ignore-host-key
proxy -ssh-insecure-allow-no-auth
```

Passed:

- Normal Windows agent completed a real SSH handshake and registered
- TCP exact echo payload `NOAUTH-NORMAL-TCP`
- UDP exact echo payload `NOAUTH-NORMAL-UDP`
- ICMP: 1 transmitted, 1 received, 0% loss
- Garble Windows agent completed a real SSH handshake
- Normal-to-Garble session and TUN recovery
- TCP exact echo payload `NOAUTH-GARBLE-TCP`
- UDP exact echo payload `NOAUTH-GARBLE-UDP`
- ICMP: 1 transmitted, 1 received, 0% loss
- TCP listener payload `NOAUTH-GARBLE-LISTENER-TCP`
- UDP listener payload `NOAUTH-GARBLE-LISTENER-UDP`
- Linux AMD64 normal and Garble agents registered without keys
- Windows and Linux no-auth agents registered simultaneously
- Linux Garble listener payload `NOAUTH-LINUX-GARBLE-LISTENER`
- Windows proxy accepted Linux normal and Garble no-auth agents through the
  test-only firewall bridge
- Linux-to-Windows TCP listener payload `NOAUTH-LINUX-TO-WINDOWS-TCP`
- Linux-to-Windows UDP listener payload `NOAUTH-LINUX-TO-WINDOWS-UDP`
- Linux Garble-to-Windows listener payload
  `NOAUTH-LINUX-GARBLE-TO-WINDOWS`

The bridge was needed only because Windows Firewall blocked direct WSL inbound
traffic and the process was not elevated. Release binaries have no dependency
on that bridge.

### Negative controls

- Unauthorized private key: rejected during SSH authentication, exit code 1
- No-auth agent against a normal authenticated proxy: rejected, exit code 1
- Incorrect username in no-auth mode: SSH handshake rejected, exit code 1
- Combining `-ssh-insecure-no-auth` with `-ssh-key`: rejected, exit code 1
- Wrong pinned host fingerprint: rejected with expected and actual
  fingerprints, exit code 1
- Agent without host verification: rejected by unit test

### Web UI and API

- Embedded UI root: HTTP 200
- Real React root present; upstream placeholder absent
- Embedded 953,260-byte JavaScript bundle: HTTP 200
- JWT login: passed
- Authenticated `/api/v1/ping`: `pong`
- `proxy.exe -version` created no config/cache/history files

The upstream Web UI placeholder was found during review and replaced by a
reproducible submodule build. React Router was updated from 6.23.0 to 6.30.4
and PostCSS from 8.4.38 to 8.5.25 without a major-version migration.

Current `npm audit --omit=dev` still reports six advisories: four high findings
in the build/tooling dependency chain (`sucrase`, `glob`, `minimatch`, and
`brace-expansion`) and two moderate React Router findings whose available fix
requires React Router 7. The major upgrade was not forced because it needs a
separate UI migration and regression cycle. Keep the Web API/UI bound to
localhost or place it behind a trusted reverse proxy.

### ARM64

- Linux ARM64 proxy executed under QEMU and printed the expected version
- Linux ARM64 normal agent completed a real no-auth SSH handshake and
  registered under QEMU
- Linux ARM64 Garble agent completed a real no-auth SSH handshake and
  registered under QEMU
- Windows ARM64 binaries received structural PE/AArch64 validation only;
  no Windows ARM64 runtime was available

## Review passes

1. Protocol/security review found Garble-renamed MsgPack field names and added
   explicit backward-compatible wire tags; it also added handshake/channel
   deadlines and strict host verification.
2. Runtime/build review found the uninitialized Web UI submodule and upstream
   dependency advisories; it added deterministic UI building, compatible
   dependency updates, and placeholder rejection.
3. Final artifact review reran source/race/vet checks, exact release runtime
   flows, Web UI/API checks, ARM64 QEMU smoke tests, architecture checks, and
   all 13 hash comparisons.

Test processes and temporary TUN interfaces were removed after verification.
Installing `qemu-user-static` in WSL was the only retained system-level test
dependency.

## ShadowPort SMB/445 verification

This section records the final `v0.9-ssh.2` candidate checks. ShadowPort uses
the existing reverse-listener stream over SSH. It source-selects inbound
TCP/445 before the PID 4 socket, translates the selected flow to an internal
port, and translates replies back to source port 445.

### Source and build checks

Passed:

- `go test ./...`
- `go vet ./...`
- `go test -race ./pkg/protocol ./pkg/agent/shadowport ./pkg/sshtransport`
- Windows AMD64 tagged test and build with `CGO_ENABLED=0`
- Web changed-file ESLint, TypeScript build, and embedded Ligolo Web build
- Protocol compatibility test for legacy listener shapes
- Tuple reuse regression test after FIN
- Source allowlist, direct-internal traffic, capacity, expiry, malformed
  packet, CIDR filter, and overlapping-scope unit tests
- Official WinDivert 2.2.2 archive, DLL, and SYS hashes matched pinned values
- WinDivert64.sys Authenticode status was valid before embedding
- Manifest recorded `working_tree_dirty=false` and
  `web_ui_working_tree_dirty=false`

The standard Windows AMD64 agent did not contain the `WinDivert64.sys` marker.
Both ShadowPort agents did. The final matrix contained 14 binaries:

- Windows AMD64: proxy, standard agent, standard Garble agent,
  `agent-shadow.exe`, and `agent-shadow-garble.exe`
- Windows ARM64, Linux AMD64, and Linux ARM64: unchanged proxy, standard agent,
  and standard Garble agent sets

PE inspection reported `0x8664` for every Windows AMD64 executable and
`0xAA64` for every Windows ARM64 executable. WSL `file` reported static x86-64
ELF for Linux AMD64 and static AArch64 ELF for Linux ARM64. All three Linux
AMD64 binaries executed in WSL and printed version
`ssh-341e1e720756`.

### Final release SSH and listener matrix

All runs used the final binaries in `release/` and the final Linux AMD64 proxy
in WSL Ubuntu:

| Agent | SSH authentication | Exact payload | Result |
|---|---|---|---|
| `agent-shadow.exe` | no-auth | `FINAL-NORMAL-NOAUTH` | passed |
| `agent-shadow-garble.exe` | no-auth | `FINAL-GARBLE-NOAUTH` | passed |
| `agent-shadow.exe` | public key + pinned host key | `FINAL-NORMAL-KEY` | passed |
| `agent-shadow-garble.exe` | public key + pinned host key | `FINAL-GARBLE-KEY` | passed |
| `agent.exe` | no-auth | `STANDARD-NORMAL-NOAUTH` | passed |
| `agent-garble.exe` | no-auth | `STANDARD-GARBLE-NOAUTH` | passed |
| `agent.exe` | public key + pinned host key | `STANDARD-NORMAL-KEY` | passed |
| `agent-garble.exe` | public key + pinned host key | `STANDARD-GARBLE-KEY` | passed |

Each ShadowPort run advertised `shadowport-smb-v1`, exposed its internal port
and source scope through the authenticated API, transported the exact payload
from WSL to Windows TCP/445 and back through SSH, removed the runtime directory
and temporary firewall rule, and left PID 4 listening on TCP/445. Standard
agents advertised no ShadowPort capability and passed the ordinary listener
regression without loading WinDivert.

### Real SMB and ntlmrelayx

An isolated WSL venv installed official Impacket 0.13.1 for test use only.
`ntlmrelayx.py` listened on `127.0.0.1:8445` with SMB2 support. An Impacket
SMB client connected from WSL to the Windows pivot address on TCP/445 through
the final `agent-shadow.exe`. The relay log recorded:

```text
[*] (SMB): Received connection from 127.0.0.1, attacking target smb://127.0.0.1:65534
```

The deliberately unreachable target then failed, which was the expected
negative control. This proves that SMB2 negotiate and NTLM reached the actual
ntlmrelayx SMB server through the SSH ShadowPort listener, not only a generic
TCP echo service.

### Driver lifecycle, forced termination, and cleanup

- Two simultaneous disjoint ShadowPort listeners shared one extracted runtime
  with reference count 2.
- Closing the first listener retained the runtime for the second listener.
- Closing the last listener removed the runtime directory and both temporary
  firewall rules.
- A forced kill after payload `FINAL-FORCED-KILL` immediately restored native
  TCP/445 reachability; PID 4 remained the owner.
- As documented, forced termination left a feature-named runtime directory and
  firewall rule because orderly cleanup could not run.
- The documented narrow cleanup commands removed both residues; verification
  then returned `RuntimeResidue=False` and `FirewallResidue=False`.

No driver, DLL, firewall rule, proxy, agent, echo server, or ntlmrelayx test
process remained after the final checks.
