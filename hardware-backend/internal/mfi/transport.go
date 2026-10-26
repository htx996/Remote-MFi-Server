// SPDX-License-Identifier: GPL-3.0-only

package mfi

import "context"

// I2CTransport performs one complete CH341-backed I2C transaction.
type I2CTransport interface {
	Transaction(context.Context, uint8, []byte, int) ([]byte, error)
	Close() error
}
