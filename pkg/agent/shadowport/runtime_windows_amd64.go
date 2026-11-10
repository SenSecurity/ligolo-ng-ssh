//go:build windows && amd64 && shadowport

package shadowport

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
)

const (
	winDivertDLLSHA256    = "c1e060ee19444a259b2162f8af0f3fe8c4428a1c6f694dce20de194ac8d7d9a2"
	winDivertDriverSHA256 = "8da085332782708d8767bcace5327a6ec7283c17cfb85e40b03cd2323a90ddc2"
	winDivertAddressSize  = 80
)

type winDivertAddress [winDivertAddressSize / 8]uint64

type winDivertAPI struct {
	dll           *windows.DLL
	open          *windows.Proc
	recv          *windows.Proc
	send          *windows.Proc
	close         *windows.Proc
	calcChecksums *windows.Proc
	driverPath    string
}

type divertHandle struct {
	api        *winDivertAPI
	inbound    windows.Handle
	outbound   windows.Handle
	translator *Translator
	done       chan struct{}
	closeOnce  sync.Once
	wait       sync.WaitGroup
}

var sharedRuntime = struct {
	sync.Mutex
	api  *winDivertAPI
	dir  string
	refs int
}{}

func supported() bool {
	return true
}

func newPacketDivert(scope listenerScope, internalPort uint16, translator *Translator) (packetDivert, error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return nil, errors.New("ShadowPort requires an elevated Administrator token")
	}
	api, err := acquireRuntime()
	if err != nil {
		return nil, err
	}

	inboundFilter := buildInboundFilter(scope)
	outboundFilter := fmt.Sprintf(
		"outbound and !impostor and ip and tcp and ip.SrcAddr == %s and tcp.SrcPort == %d",
		scope.publicAddress, internalPort,
	)
	inbound, err := api.openHandle(inboundFilter)
	if err != nil {
		_ = releaseRuntime()
		return nil, fmt.Errorf("open ShadowPort inbound diversion: %w", err)
	}
	outbound, err := api.openHandle(outboundFilter)
	if err != nil {
		api.closeHandle(inbound)
		_ = releaseRuntime()
		return nil, fmt.Errorf("open ShadowPort outbound diversion: %w", err)
	}

	divert := &divertHandle{
		api:        api,
		inbound:    inbound,
		outbound:   outbound,
		translator: translator,
		done:       make(chan struct{}),
	}
	api.hideDriverFile()
	divert.wait.Add(3)
	go divert.packetLoop(inbound, false)
	go divert.packetLoop(outbound, true)
	go divert.expiryLoop()
	logrus.Info("Scoped packet diversion activated")
	return divert, nil
}

func buildInboundFilter(scope listenerScope) string {
	sources := make([]string, 0, len(scope.sources))
	for _, source := range scope.sources {
		sources = append(sources, sourceFilterExpression(source))
	}
	return fmt.Sprintf(
		"inbound and !impostor and ip and tcp and ip.DstAddr == %s and tcp.DstPort == %d and (%s)",
		scope.publicAddress, scope.publicPort, strings.Join(sources, " or "),
	)
}

func (d *divertHandle) packetLoop(handle windows.Handle, outbound bool) {
	defer d.wait.Done()
	packet := make([]byte, 0xffff+40)
	for {
		var recvLength uint32
		var address winDivertAddress
		ok, err := d.api.recvPacket(handle, packet, &recvLength, &address)
		if !ok {
			if err != nil && !errors.Is(err, windows.ERROR_INVALID_HANDLE) &&
				!errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
				logrus.WithError(err).Debug("Divert receive loop stopped")
			}
			return
		}
		current := packet[:recvLength]
		decision := d.translator.Translate(current, outbound, time.Now())
		if decision == CapacityExceeded {
			logrus.Warn("Flow table capacity reached; dropping new selected flow")
			continue
		}
		if decision == Rewritten {
			if err := d.api.calculateChecksums(current, &address); err != nil {
				logrus.WithError(err).Error("Checksum recalculation failed; dropping packet")
				continue
			}
		}
		if err := d.api.sendPacket(handle, current, &address); err != nil {
			if !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
				logrus.WithError(err).Debug("Divert reinjection stopped")
			}
			return
		}
	}
}

func (d *divertHandle) expiryLoop() {
	defer d.wait.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			d.translator.Expire(time.Now())
		case <-d.done:
			return
		}
	}
}

func (d *divertHandle) Close() error {
	var closeErr error
	d.closeOnce.Do(func() {
		close(d.done)
		if err := d.api.closeHandle(d.inbound); err != nil {
			closeErr = err
		}
		if err := d.api.closeHandle(d.outbound); closeErr == nil {
			closeErr = err
		}
		d.wait.Wait()
		if err := releaseRuntime(); closeErr == nil {
			closeErr = err
		}
	})
	return closeErr
}

func acquireRuntime() (*winDivertAPI, error) {
	sharedRuntime.Lock()
	defer sharedRuntime.Unlock()
	if sharedRuntime.api != nil {
		sharedRuntime.refs++
		return sharedRuntime.api, nil
	}
	api, dir, err := prepareRuntime()
	if err != nil {
		return nil, err
	}
	sharedRuntime.api = api
	sharedRuntime.dir = dir
	sharedRuntime.refs = 1
	logrus.Debug("Divert runtime prepared")
	return api, nil
}

func releaseRuntime() error {
	sharedRuntime.Lock()
	defer sharedRuntime.Unlock()
	if sharedRuntime.api == nil || sharedRuntime.refs == 0 {
		return nil
	}
	sharedRuntime.refs--
	if sharedRuntime.refs > 0 {
		return nil
	}
	api := sharedRuntime.api
	dir := sharedRuntime.dir
	sharedRuntime.api = nil
	sharedRuntime.dir = ""

	releaseErr := api.dll.Release()
	removeErr := wipeRuntimeArtifacts(dir)
	if removeErr != nil {
		logrus.WithError(removeErr).Warn("Divert runtime residue remains after cleanup")
	} else {
		logrus.Debug("Divert runtime removed")
	}
	return errors.Join(releaseErr, removeErr)
}

func prepareRuntime() (*winDivertAPI, string, error) {
	if err := verifyBytes(embeddedWinDivertDLL, winDivertDLLSHA256, "WinDivert.dll"); err != nil {
		return nil, "", err
	}
	if err := verifyBytes(embeddedWinDivertDriver, winDivertDriverSHA256, "WinDivert64.sys"); err != nil {
		return nil, "", err
	}
	// The directory name is a bare random value with no product prefix. The
	// official DLL locates the driver image next to itself and requires the
	// canonical file names, so the artifacts themselves cannot be renamed.
	dir, err := os.MkdirTemp("", "")
	if err != nil {
		return nil, "", fmt.Errorf("create ShadowPort runtime directory: %w", err)
	}
	fail := func(err error) (*winDivertAPI, string, error) {
		wipeRuntimeArtifacts(dir)
		return nil, "", err
	}
	if err := hardenDirectory(dir); err != nil {
		return fail(fmt.Errorf("protect ShadowPort runtime directory: %w", err))
	}
	dllPath := filepath.Join(dir, "WinDivert.dll")
	driverPath := filepath.Join(dir, "WinDivert64.sys")
	if err := os.WriteFile(dllPath, embeddedWinDivertDLL, 0o600); err != nil {
		return fail(fmt.Errorf("extract WinDivert.dll: %w", err))
	}
	markTemporary(dllPath)
	if err := os.WriteFile(driverPath, embeddedWinDivertDriver, 0o600); err != nil {
		return fail(fmt.Errorf("extract WinDivert64.sys: %w", err))
	}
	markTemporary(driverPath)
	if err := verifyFile(dllPath, winDivertDLLSHA256); err != nil {
		return fail(err)
	}
	if err := verifyFile(driverPath, winDivertDriverSHA256); err != nil {
		return fail(err)
	}
	if err := verifyAuthenticode(driverPath); err != nil {
		return fail(fmt.Errorf("verify WinDivert64.sys signature: %w", err))
	}
	api, err := loadWinDivert(dllPath)
	if err != nil {
		return fail(err)
	}
	api.driverPath = driverPath
	return api, dir, nil
}

// hideDriverFile removes the driver image from disk once the kernel service
// has been installed and started for the current handles. The service keeps
// running from the image the kernel already loaded, so the file is not read
// again until the last filter handle closes and the DLL uninstalls the
// service. If the removal fails (for example a short-lived antivirus scan
// holds the file), the teardown wipe still clears it. When the runtime is
// re-acquired after every handle was closed, prepareRuntime extracts the
// files again.
func (api *winDivertAPI) hideDriverFile() {
	sharedRuntime.Lock()
	defer sharedRuntime.Unlock()
	if api.driverPath == "" {
		return
	}
	if err := os.Remove(api.driverPath); err == nil {
		api.driverPath = ""
	}
}

// wipeRuntimeArtifacts overwrites the dropped runtime files before removing
// the directory, so no intact driver or DLL image remains on disk after a
// normal teardown. The retry loop covers short-lived antivirus locks; if the
// directory still survives, the caller reports residue.
func wipeRuntimeArtifacts(dir string) error {
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				zeroFile(filepath.Join(dir, entry.Name()))
			}
		}
	}
	var removeErr error
	for attempt := 0; attempt < 3; attempt++ {
		if removeErr = os.RemoveAll(dir); removeErr == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return removeErr
}

func zeroFile(path string) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer file.Close()
	buffer := make([]byte, 128*1024)
	remaining := info.Size()
	for remaining > 0 {
		chunk := int64(len(buffer))
		if remaining < chunk {
			chunk = remaining
		}
		written, err := file.Write(buffer[:chunk])
		if err != nil {
			return
		}
		remaining -= int64(written)
	}
}

// markTemporary sets FILE_ATTRIBUTE_TEMPORARY so the cache manager favors
// keeping the short-lived runtime artifacts in memory instead of flushing
// them to disk.
func markTemporary(path string) {
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	_ = windows.SetFileAttributes(pathUTF16, windows.FILE_ATTRIBUTE_TEMPORARY)
}

func loadWinDivert(path string) (*winDivertAPI, error) {
	dll, err := windows.LoadDLL(path)
	if err != nil {
		return nil, fmt.Errorf("load WinDivert.dll: %w", err)
	}
	find := func(name string) (*windows.Proc, error) {
		proc, findErr := dll.FindProc(name)
		if findErr != nil {
			return nil, fmt.Errorf("resolve %s: %w", name, findErr)
		}
		return proc, nil
	}
	api := &winDivertAPI{dll: dll}
	if api.open, err = find("WinDivertOpen"); err == nil {
		api.recv, err = find("WinDivertRecv")
	}
	if err == nil {
		api.send, err = find("WinDivertSend")
	}
	if err == nil {
		api.close, err = find("WinDivertClose")
	}
	if err == nil {
		api.calcChecksums, err = find("WinDivertHelperCalcChecksums")
	}
	if err != nil {
		dll.Release()
		return nil, err
	}
	return api, nil
}

func (api *winDivertAPI) openHandle(filter string) (windows.Handle, error) {
	filterBytes, err := windows.BytePtrFromString(filter)
	if err != nil {
		return 0, err
	}
	result, _, callErr := api.open.Call(uintptr(unsafe.Pointer(filterBytes)), 0, 0, 0)
	runtime.KeepAlive(filterBytes)
	handle := windows.Handle(result)
	if handle == windows.InvalidHandle {
		return 0, callError(callErr)
	}
	return handle, nil
}

func (api *winDivertAPI) recvPacket(handle windows.Handle, packet []byte, recvLength *uint32, address *winDivertAddress) (bool, error) {
	result, _, callErr := api.recv.Call(
		uintptr(handle), uintptr(unsafe.Pointer(&packet[0])), uintptr(len(packet)),
		uintptr(unsafe.Pointer(recvLength)), uintptr(unsafe.Pointer(&address[0])),
	)
	runtime.KeepAlive(packet)
	runtime.KeepAlive(address)
	if result == 0 {
		return false, callError(callErr)
	}
	return true, nil
}

func (api *winDivertAPI) sendPacket(handle windows.Handle, packet []byte, address *winDivertAddress) error {
	var sentLength uint32
	result, _, callErr := api.send.Call(
		uintptr(handle), uintptr(unsafe.Pointer(&packet[0])), uintptr(len(packet)),
		uintptr(unsafe.Pointer(&sentLength)), uintptr(unsafe.Pointer(&address[0])),
	)
	runtime.KeepAlive(packet)
	runtime.KeepAlive(address)
	if result == 0 {
		return callError(callErr)
	}
	if sentLength != uint32(len(packet)) {
		return fmt.Errorf("short WinDivert send: %d of %d bytes", sentLength, len(packet))
	}
	return nil
}

func (api *winDivertAPI) calculateChecksums(packet []byte, address *winDivertAddress) error {
	result, _, callErr := api.calcChecksums.Call(
		uintptr(unsafe.Pointer(&packet[0])), uintptr(len(packet)),
		uintptr(unsafe.Pointer(&address[0])), 0,
	)
	runtime.KeepAlive(packet)
	runtime.KeepAlive(address)
	if result == 0 {
		return callError(callErr)
	}
	return nil
}

func (api *winDivertAPI) closeHandle(handle windows.Handle) error {
	if handle == 0 || handle == windows.InvalidHandle {
		return nil
	}
	result, _, callErr := api.close.Call(uintptr(handle))
	if result == 0 {
		return callError(callErr)
	}
	return nil
}

func callError(err error) error {
	if err == nil || errors.Is(err, windows.ERROR_SUCCESS) {
		return errors.New("WinDivert call failed without a Windows error")
	}
	return err
}

func verifyBytes(data []byte, expectedHex, name string) error {
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return err
	}
	actual := sha256.Sum256(data)
	if !equalBytes(actual[:], expected) {
		return fmt.Errorf("%s embedded SHA256 mismatch: %x", name, actual)
	}
	return nil
}

func verifyFile(path, expectedHex string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return verifyBytes(data, expectedHex, filepath.Base(path))
}

func equalBytes(left, right []byte) bool {
	return subtle.ConstantTimeCompare(left, right) == 1
}

func verifyAuthenticode(path string) error {
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	fileInfo := windows.WinTrustFileInfo{
		Size:     uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})),
		FilePath: pathUTF16,
	}
	data := windows.WinTrustData{
		Size:                            uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                windows.WTD_REVOKE_NONE,
		UnionChoice:                     windows.WTD_CHOICE_FILE,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&fileInfo),
		StateAction:                     windows.WTD_STATEACTION_VERIFY,
		ProvFlags:                       windows.WTD_CACHE_ONLY_URL_RETRIEVAL,
	}
	verifyErr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data)
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	closeErr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data)
	runtime.KeepAlive(fileInfo)
	if verifyErr != nil {
		return verifyErr
	}
	return closeErr
}

func hardenDirectory(path string) error {
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
}
