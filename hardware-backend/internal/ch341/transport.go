// SPDX-License-Identifier: GPL-3.0-only

package ch341

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/htx996/remote-mfi-server/hardware-backend/internal/mfi"
)

const (
	defaultTransferTimeout = time.Second
	responseQuietTimeout   = 25 * time.Millisecond
	selectToReadGap        = 5 * time.Millisecond
	maxUSBPacketBytes      = 32
)

var (
	ErrUSBPermission = errors.New("USB permission denied")
	ErrUSBBusy       = errors.New("USB device busy")
	ErrUSBMissing    = errors.New("USB device not found")
	ErrI2CNACK       = errors.New("I2C NACK")
)

type bulkSession interface {
	Write(context.Context, []byte) (int, error)
	Read(context.Context, []byte) (int, error)
	Close() error
}

// Transport serializes CH341 configuration, stream writes, and replies over
// one claimed USB bulk session.
type Transport struct {
	mu      sync.Mutex
	session bulkSession
	speed   int
	wait    func(context.Context, time.Duration) error
	closed  bool
}

var _ mfi.I2CTransport = (*Transport)(nil)

func newTransport(session bulkSession, speed int, options ...func(*Transport)) (*Transport, error) {
	if session == nil {
		return nil, fmt.Errorf("USB bulk session is required")
	}
	if _, err := EncodeConfiguration(speed); err != nil {
		return nil, err
	}
	transport := &Transport{session: session, speed: speed, wait: waitForContext}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("transport option must not be nil")
		}
		option(transport)
	}
	return transport, nil
}

// Transaction performs one complete I2C operation. A pure read is delayed by
// at least 5 ms because MFi register reads are selected in the preceding,
// separate write-only transaction.
func (t *Transport) Transaction(
	ctx context.Context,
	address uint8,
	writeData []byte,
	readLength int,
) ([]byte, error) {
	stream, err := EncodeTransaction(address, writeData, readLength)
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, fmt.Errorf("CH341 USB session is closed")
	}
	configuration, err := EncodeConfiguration(t.speed)
	if err != nil {
		return nil, err
	}
	timeout := minimumTransferTimeout(len(writeData), readLength, t.speed)
	if err := t.writeAll(ctx, configuration, timeout); err != nil {
		return nil, t.closeOnContextError(err)
	}
	if len(writeData) == 0 && readLength > 0 {
		if err := t.wait(ctx, selectToReadGap); err != nil {
			return nil, t.closeOnContextError(err)
		}
	}

	response := make([]byte, 0, readLength)
	for offset := 0; offset < len(stream); {
		end := min(offset+maxStreamPacketBytes, len(stream))
		segment := stream[offset:end]
		if err := t.writeAll(ctx, segment, timeout); err != nil {
			return nil, t.closeOnContextError(err)
		}
		statusMax, dataBytes, err := InstructionLengths(segment)
		if err != nil {
			return nil, err
		}
		if statusMax+dataBytes > 0 {
			firstTimeout := responseQuietTimeout
			if dataBytes > 0 {
				firstTimeout = timeout
			}
			received, err := t.readAtMost(ctx, statusMax+dataBytes, firstTimeout, dataBytes == 0)
			if err != nil {
				return nil, t.closeOnContextError(err)
			}
			data, err := ExtractReadData(received, dataBytes)
			if err != nil {
				return nil, err
			}
			response = append(response, data...)
		}
		offset = end
	}
	if len(response) != readLength {
		return nil, fmt.Errorf("CH341 transaction returned %d bytes; expected %d", len(response), readLength)
	}
	return response, nil
}

// Close releases the claimed interface and every owning libusb object.
func (t *Transport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closeLocked()
}

func (t *Transport) closeLocked() error {
	if t.closed {
		return nil
	}
	t.closed = true
	return t.session.Close()
}

func (t *Transport) closeOnContextError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		_ = t.closeLocked()
	}
	return err
}

func (t *Transport) writeAll(ctx context.Context, data []byte, timeout time.Duration) error {
	transferContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	written, err := t.session.Write(transferContext, data)
	if err != nil {
		return fmt.Errorf("CH341 bulk write: %w", err)
	}
	if written != len(data) {
		return fmt.Errorf("CH341 bulk write transferred %d of %d bytes", written, len(data))
	}
	return nil
}

func (t *Transport) readAtMost(
	ctx context.Context,
	maximum int,
	firstTimeout time.Duration,
	allowEmpty bool,
) ([]byte, error) {
	data := make([]byte, 0, maximum)
	for len(data) < maximum {
		timeout := responseQuietTimeout
		if len(data) == 0 {
			timeout = firstTimeout
		}
		readContext, cancel := context.WithTimeout(ctx, timeout)
		buffer := make([]byte, min(maximum-len(data), maxUSBPacketBytes))
		count, err := t.session.Read(readContext, buffer)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && (allowEmpty || len(data) > 0) && ctx.Err() == nil {
				break
			}
			return nil, fmt.Errorf("CH341 bulk read: %w", err)
		}
		if count <= 0 {
			if allowEmpty || len(data) > 0 {
				break
			}
			return nil, fmt.Errorf("CH341 bulk read returned no data")
		}
		if count > len(buffer) {
			return nil, fmt.Errorf("CH341 bulk read returned invalid length %d", count)
		}
		data = append(data, buffer[:count]...)
	}
	return data, nil
}

func minimumTransferTimeout(writeLength, readLength, speedKHz int) time.Duration {
	const (
		transactionOverheadBytes = 8
		bitsPerI2CByte           = 9
	)
	bitsPerSecond := int64(speedKHz) * 1000
	bytesOnWire := int64(writeLength + readLength + transactionOverheadBytes)
	wire := time.Duration((bytesOnWire*bitsPerI2CByte*int64(time.Second) + bitsPerSecond - 1) / bitsPerSecond)
	return max(defaultTransferTimeout, wire+defaultTransferTimeout)
}

func waitForContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func safeUSBError(operation string, err error) error {
	switch {
	case errors.Is(err, ErrUSBPermission):
		return fmt.Errorf("%s: %w", operation, ErrUSBPermission)
	case errors.Is(err, ErrUSBBusy):
		return fmt.Errorf("%s: %w", operation, ErrUSBBusy)
	default:
		return fmt.Errorf("%s: USB operation failed", operation)
	}
}
