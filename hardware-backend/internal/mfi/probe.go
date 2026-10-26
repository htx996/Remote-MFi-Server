// SPDX-License-Identifier: GPL-3.0-only

package mfi

import (
	"context"
	"fmt"
)

// ProbeResult contains only non-sensitive identity and size information.
type ProbeResult struct {
	Address           uint8
	DeviceVersion     uint8
	ProtocolMajor     uint8
	CertificateLength uint16
}

// Probe verifies that the configured MFi address exposes plausible identity
// registers and a bounded accessory certificate.
func (d *Driver) Probe(ctx context.Context) (ProbeResult, error) {
	deviceVersion, err := d.readByte(ctx, deviceVersionRegister)
	if err != nil {
		return ProbeResult{}, err
	}
	if deviceVersion == 0x00 || deviceVersion == 0xff {
		return ProbeResult{}, fmt.Errorf("invalid MFi device version 0x%02x", deviceVersion)
	}
	protocolMajor, err := d.readByte(ctx, protocolMajorRegister)
	if err != nil {
		return ProbeResult{}, err
	}
	if protocolMajor == 0x00 || protocolMajor == 0xff {
		return ProbeResult{}, fmt.Errorf("invalid MFi protocol major 0x%02x", protocolMajor)
	}
	certificateLength, err := d.readUint16(ctx, certificateLengthRegister)
	if err != nil {
		return ProbeResult{}, err
	}
	if certificateLength < 1 || certificateLength > maximumCertificateBytes {
		return ProbeResult{}, fmt.Errorf(
			"certificate length %d is outside 1..%d",
			certificateLength,
			maximumCertificateBytes,
		)
	}
	return ProbeResult{
		Address:           d.address,
		DeviceVersion:     deviceVersion,
		ProtocolMajor:     protocolMajor,
		CertificateLength: certificateLength,
	}, nil
}
