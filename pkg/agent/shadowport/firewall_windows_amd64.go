//go:build windows && amd64 && shadowport

package shadowport

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
)

// INetFwRule property values used by the scoped inbound exception.
const (
	netFwRuleDirectionInbound = 1 // NET_FW_RULE_DIR_IN
	netFwRuleActionAllow      = 1 // NET_FW_ACTION_ALLOW
	netFwProtocolTCP          = 6 // IPPROTO_TCP
	netFwProfile2All          = 0x7FFFFFFF
)

var (
	ole32DLL          = windows.NewLazySystemDLL("ole32.dll")
	coInitializeExPtr = ole32DLL.NewProc("CoInitializeEx")
	coUninitializePtr = ole32DLL.NewProc("CoUninitialize")
)

type windowsFirewallPermit struct {
	name      string
	closeOnce sync.Once
}

// newFirewallPermit registers a scoped inbound TCP allow rule through the
// native Windows Firewall COM API (INetFwPolicy2). No helper process is
// spawned, so no script interpreter command line becomes visible in process
// telemetry. The rule name and display name are one random hex token that does
// not identify the tool; the rule is removed when the permit closes.
func newFirewallPermit(scope listenerScope, internalPort uint16) (packetDivert, error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return nil, errors.New("ShadowPort requires an elevated Administrator token")
	}
	token, err := opaqueToken(8)
	if err != nil {
		return nil, err
	}
	remoteAddresses := make([]string, 0, len(scope.sources))
	for _, source := range scope.sources {
		remoteAddresses = append(remoteAddresses, source.String())
	}

	rules, done, err := firewallRules()
	if err != nil {
		return nil, err
	}
	defer done()

	rule, err := oleutil.CreateObject("HNetCfg.FwRule")
	if err != nil {
		return nil, fmt.Errorf("create firewall rule object: %w", err)
	}
	defer rule.Release()
	ruleDispatch, err := rule.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return nil, fmt.Errorf("query firewall rule interface: %w", err)
	}
	defer ruleDispatch.Release()

	settings := []struct {
		property string
		value    interface{}
	}{
		{"Name", token},
		{"DisplayName", token},
		{"Enabled", true},
		{"Profiles", int32(netFwProfile2All)},
		{"Direction", int32(netFwRuleDirectionInbound)},
		{"Action", int32(netFwRuleActionAllow)},
		{"Protocol", int32(netFwProtocolTCP)},
		{"LocalAddresses", scope.publicAddress.String()},
		{"LocalPorts", strconv.Itoa(int(internalPort))},
		{"RemoteAddresses", strings.Join(remoteAddresses, ",")},
	}
	for _, setting := range settings {
		if _, err := oleutil.PutProperty(ruleDispatch, setting.property, setting.value); err != nil {
			return nil, fmt.Errorf("set firewall rule property %q: %w", setting.property, err)
		}
	}
	added, err := oleutil.CallMethod(rules, "Add", ruleDispatch)
	if err != nil {
		return nil, fmt.Errorf("create scoped firewall exception: %w", err)
	}
	added.Clear()
	logrus.Debug("Scoped inbound firewall exception created")
	return &windowsFirewallPermit{name: token}, nil
}

func (f *windowsFirewallPermit) Close() error {
	var closeErr error
	f.closeOnce.Do(func() {
		rules, done, err := firewallRules()
		if err != nil {
			closeErr = err
			return
		}
		defer done()
		removed, err := oleutil.CallMethod(rules, "Remove", f.name)
		if err != nil {
			closeErr = fmt.Errorf("remove scoped firewall exception %q: %w", f.name, err)
			return
		}
		removed.Clear()
		logrus.Debug("Scoped inbound firewall exception removed")
	})
	return closeErr
}

// firewallRules returns the INetFwPolicy2 rule collection together with a
// release function that releases the collection and tears down the COM
// apartment. The release function must run only after the last use of the
// collection, because the returned pointer borrows the VARIANT reference.
func firewallRules() (*ole.IDispatch, func(), error) {
	apartment, err := enterCOMApartment()
	if err != nil {
		return nil, nil, err
	}
	leave := apartment.leave

	policy, err := oleutil.CreateObject("HNetCfg.FwPolicy2")
	if err != nil {
		leave()
		return nil, nil, fmt.Errorf("create firewall policy object: %w", err)
	}
	policyDispatch, err := policy.QueryInterface(ole.IID_IDispatch)
	policy.Release()
	if err != nil {
		leave()
		return nil, nil, fmt.Errorf("query firewall policy interface: %w", err)
	}
	rulesVariant, err := oleutil.GetProperty(policyDispatch, "Rules")
	policyDispatch.Release()
	if err != nil {
		leave()
		return nil, nil, fmt.Errorf("resolve firewall rule collection: %w", err)
	}
	rules := rulesVariant.ToIDispatch()
	if rules == nil {
		rulesVariant.Clear()
		leave()
		return nil, nil, errors.New("firewall rule collection is unavailable")
	}
	release := func() {
		rulesVariant.Clear()
		leave()
	}
	return rules, release, nil
}

// comApartment pins the goroutine to one OS thread and initializes COM on it
// for the duration of one firewall operation. A multithreaded apartment keeps
// the direct vtable calls valid; locking the thread prevents the Go scheduler
// from moving the calls onto an uninitialized thread.
type comApartment struct{}

func enterCOMApartment() (comApartment, error) {
	runtime.LockOSThread()
	result, _, _ := coInitializeExPtr.Call(0, ole.COINIT_MULTITHREADED)
	// S_OK (0) and S_FALSE (1) both require a matching CoUninitialize.
	if result != 0 && result != 1 {
		runtime.UnlockOSThread()
		return comApartment{}, fmt.Errorf("initialize COM apartment (0x%08x)", result)
	}
	return comApartment{}, nil
}

func (comApartment) leave() {
	_, _, _ = coUninitializePtr.Call()
	runtime.UnlockOSThread()
}
