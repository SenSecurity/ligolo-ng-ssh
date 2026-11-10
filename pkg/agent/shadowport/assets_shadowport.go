//go:build windows && amd64 && shadowport

package shadowport

import _ "embed"

//go:embed assets/bin/WinDivert.dll
var embeddedWinDivertDLL []byte

//go:embed assets/bin/WinDivert64.sys
var embeddedWinDivertDriver []byte
