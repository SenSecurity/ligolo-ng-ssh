//go:build !windows || !amd64 || !shadowport

package shadowport

import "errors"

func supported() bool {
	return false
}

func newPacketDivert(listenerScope, uint16, *Translator) (packetDivert, error) {
	return nil, errors.New("ShadowPort is unsupported by this agent build")
}

func newFirewallPermit(listenerScope, uint16) (packetDivert, error) {
	return nil, errors.New("ShadowPort is unsupported by this agent build")
}
