//go:build cgo

package ch341

import (
	"context"
	"errors"
	"testing"

	"github.com/google/gousb"
)

func TestDisconnectedDeviceIsClassifiedAsMissing(t *testing.T) {
	for _, input := range []error{gousb.ErrorNoDevice, gousb.ErrorNotFound, gousb.TransferNoDevice} {
		if got := transferError(context.Background(), input); !errors.Is(got, ErrUSBMissing) {
			t.Errorf("transferError(%v) = %v, want ErrUSBMissing", input, got)
		}
	}
}
