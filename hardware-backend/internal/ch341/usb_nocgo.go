// SPDX-License-Identifier: GPL-3.0-only

//go:build !cgo

package ch341

import (
	"context"
	"fmt"

	"github.com/htx996/remote-mfi-server/hardware-backend/internal/config"
	"github.com/htx996/remote-mfi-server/hardware-backend/internal/mfi"
)

// Open is a build-capability stub for unit-test environments. Production
// images build with CGO and libusb and therefore use usb_cgo.go instead.
func Open(_ context.Context, _ config.Config) (mfi.I2CTransport, error) {
	return nil, fmt.Errorf("CH341 libusb backend is unavailable in a non-CGO build")
}
