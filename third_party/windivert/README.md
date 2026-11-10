# WinDivert runtime

ShadowPort uses the untouched official WinDivert 2.2.2 AMD64 runtime:

- Source: https://github.com/basil00/WinDivert
- Release archive: https://github.com/basil00/WinDivert/releases/download/v2.2.2/WinDivert-2.2.2-A.zip
- Archive SHA-256: `63cb41763bb4b20f600b6de04e991a9c2be73279e317d4d82f237b150c5f3f15`
- `WinDivert.dll` SHA-256: `c1e060ee19444a259b2162f8af0f3fe8c4428a1c6f694dce20de194ac8d7d9a2`
- `WinDivert64.sys` SHA-256: `8da085332782708d8767bcace5327a6ec7283c17cfb85e40b03cd2323a90ddc2`

Run `scripts/fetch-windivert.ps1` before a ShadowPort build. The script verifies
the pinned hashes and Windows Authenticode signatures, stages the runtime for
Go embedding, and copies the upstream license to this directory.

The driver is not modified, renamed, manually mapped, or used for security
product bypass. It is extracted to disk only when ShadowPort is activated
because supported Windows kernel-driver loading requires a filesystem image.
