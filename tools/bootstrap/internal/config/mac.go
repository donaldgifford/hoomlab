package config

import (
	"fmt"
	"net"
)

// NormalizeMAC parses s as a 48-bit MAC address and returns it in the
// canonical form used everywhere downstream: lowercase, colon-separated
// (net.HardwareAddr.String's format). The VM Net0 macaddr and the
// emitted booty group selector both derive from this one form, so the
// PXE identity binding agrees by construction (DESIGN-0001 OQ-5).
func NormalizeMAC(s string) (string, error) {
	hw, err := net.ParseMAC(s)
	if err != nil {
		return "", fmt.Errorf("parse mac %q: %w", s, err)
	}
	if len(hw) != 6 {
		return "", fmt.Errorf("mac %q: must be a 48-bit address, got %d bytes", s, len(hw))
	}
	return hw.String(), nil
}
