// SPDX-License-Identifier: GPL-3.0-only

package mfi

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"
)

const (
	deviceVersionRegister     = 0x00
	protocolMajorRegister     = 0x02
	errorRegister             = 0x05
	authControlStatusRegister = 0x10
	responseLengthRegister    = 0x11
	responseDataRegister      = 0x12
	challengeLengthRegister   = 0x20
	challengeDataRegister     = 0x21
	certificateLengthRegister = 0x30
	certificateDataRegister   = 0x31
	certificateWindowBytes    = 128
	maximumCertificateBytes   = 1280
	maximumChallengeBytes     = 128
	authStartCommand          = 0x01
	authSuccessStatus         = 0x10
	defaultIORetryTimeout     = 2 * time.Second
	defaultIORetryDelay       = 20 * time.Millisecond
	defaultInitialAuthDelay   = 10 * time.Millisecond
	defaultAuthPollInterval   = 10 * time.Millisecond
	defaultAuthTimeout        = 3 * time.Second
)

// Option adjusts deterministic driver timing, primarily for tests.
type Option func(*Driver) error

// WithRetryTiming replaces the select/read retry window and interval.
func WithRetryTiming(timeout, delay time.Duration) Option {
	return func(driver *Driver) error {
		if timeout < 0 || delay < 0 {
			return fmt.Errorf("retry timing must not be negative")
		}
		driver.ioRetryTimeout = timeout
		driver.ioRetryDelay = delay
		return nil
	}
}

// WithAuthenticationTiming replaces the initial wait, polling interval, and
// authentication timeout. Production uses the MFi-defined defaults.
func WithAuthenticationTiming(initialDelay, pollInterval, timeout time.Duration) Option {
	return func(driver *Driver) error {
		if initialDelay < 0 || pollInterval < 0 || timeout < 0 {
			return fmt.Errorf("authentication timing must not be negative")
		}
		driver.initialAuthDelay = initialDelay
		driver.authPollInterval = pollInterval
		driver.authTimeout = timeout
		return nil
	}
}

// Driver implements the MFi authentication coprocessor register protocol.
type Driver struct {
	transport        I2CTransport
	address          uint8
	ioRetryTimeout   time.Duration
	ioRetryDelay     time.Duration
	initialAuthDelay time.Duration
	authPollInterval time.Duration
	authTimeout      time.Duration
	now              func() time.Time
	wait             func(context.Context, time.Duration) error
}

// NewDriver binds one configured 7-bit address to one transport. It never
// discovers or silently changes the business address.
func NewDriver(transport I2CTransport, address uint8, options ...Option) (*Driver, error) {
	if transport == nil {
		return nil, fmt.Errorf("I2C transport is required")
	}
	if address > 0x7f {
		return nil, fmt.Errorf("I2C address must be a 7-bit value")
	}
	driver := &Driver{
		transport:        transport,
		address:          address,
		ioRetryTimeout:   defaultIORetryTimeout,
		ioRetryDelay:     defaultIORetryDelay,
		initialAuthDelay: defaultInitialAuthDelay,
		authPollInterval: defaultAuthPollInterval,
		authTimeout:      defaultAuthTimeout,
		now:              time.Now,
		wait:             waitContext,
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("driver option must not be nil")
		}
		if err := option(driver); err != nil {
			return nil, err
		}
	}
	return driver, nil
}

// Close releases the underlying USB/I2C transport.
func (d *Driver) Close() error { return d.transport.Close() }

// ReadCertificate reads the real accessory certificate from incrementing
// 128-byte MFi data windows. Certificate bytes are never included in errors.
func (d *Driver) ReadCertificate(ctx context.Context) ([]byte, error) {
	length, err := d.readUint16(ctx, certificateLengthRegister)
	if err != nil {
		return nil, err
	}
	if length < 1 || length > maximumCertificateBytes {
		return nil, fmt.Errorf("certificate length %d is outside 1..%d", length, maximumCertificateBytes)
	}

	certificate := make([]byte, int(length))
	for offset, register := 0, byte(certificateDataRegister); offset < len(certificate); register++ {
		count := min(certificateWindowBytes, len(certificate)-offset)
		window, err := d.readRegister(ctx, register, count)
		if err != nil {
			return nil, err
		}
		copy(certificate[offset:], window)
		offset += count
	}
	return certificate, nil
}

// SignChallenge asks the physical MFi coprocessor to sign one challenge and
// returns only the bytes read from its response data register.
func (d *Driver) SignChallenge(ctx context.Context, challenge []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(challenge) < 1 || len(challenge) > maximumChallengeBytes {
		return nil, fmt.Errorf("challenge must be 1..%d bytes", maximumChallengeBytes)
	}
	if err := d.writeUint16(ctx, challengeLengthRegister, uint16(len(challenge))); err != nil {
		return nil, err
	}
	if err := d.writeRegister(ctx, challengeDataRegister, challenge); err != nil {
		return nil, err
	}
	if err := d.writeRegister(ctx, authControlStatusRegister, []byte{authStartCommand}); err != nil {
		return nil, err
	}
	if err := d.wait(ctx, d.initialAuthDelay); err != nil {
		return nil, err
	}

	deadline := d.now().Add(d.authTimeout)
	for {
		status, err := d.readByte(ctx, authControlStatusRegister)
		if err == nil && status == authSuccessStatus {
			break
		}
		if !d.now().Before(deadline) {
			if code, codeErr := d.bestEffortErrorCode(ctx); codeErr == nil {
				return nil, fmt.Errorf("MFi authentication timed out after %s with MFi error 0x%02x", d.authTimeout, code)
			}
			return nil, fmt.Errorf("MFi authentication timed out after %s", d.authTimeout)
		}
		if err := d.wait(ctx, d.authPollInterval); err != nil {
			return nil, err
		}
	}

	length, err := d.readUint16(ctx, responseLengthRegister)
	if err != nil {
		return nil, err
	}
	if length < 1 || length > maximumCertificateBytes {
		return nil, fmt.Errorf("signature length %d is outside 1..%d", length, maximumCertificateBytes)
	}
	return d.readRegister(ctx, responseDataRegister, int(length))
}

func (d *Driver) bestEffortErrorCode(ctx context.Context) (byte, error) {
	return d.readByte(ctx, errorRegister)
}

func (d *Driver) writeUint16(ctx context.Context, register byte, value uint16) error {
	data := []byte{byte(value >> 8), byte(value)}
	return d.writeRegister(ctx, register, data)
}

func (d *Driver) writeRegister(ctx context.Context, register byte, data []byte) error {
	if len(data) > maximumCertificateBytes {
		return fmt.Errorf("write register 0x%02x length %d is outside 0..%d", register, len(data), maximumCertificateBytes)
	}
	request := make([]byte, len(data)+1)
	request[0] = register
	copy(request[1:], data)
	deadline := d.now().Add(d.ioRetryTimeout)
	var lastError error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := d.transport.Transaction(ctx, d.address, request, 0)
		if err == nil && len(result) != 0 {
			err = fmt.Errorf("register write returned unexpected data")
		}
		if err == nil {
			return nil
		}
		lastError = err
		if !d.now().Before(deadline) {
			return fmt.Errorf("write register 0x%02x: %w", register, lastError)
		}
		if err := d.wait(ctx, d.ioRetryDelay); err != nil {
			return err
		}
	}
}

func (d *Driver) readByte(ctx context.Context, register byte) (byte, error) {
	data, err := d.readRegister(ctx, register, 1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

func (d *Driver) readUint16(ctx context.Context, register byte) (uint16, error) {
	data, err := d.readRegister(ctx, register, 2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(data), nil
}

func (d *Driver) readRegister(ctx context.Context, register byte, length int) ([]byte, error) {
	if length < 1 || length > maximumCertificateBytes {
		return nil, fmt.Errorf("read register 0x%02x length %d is outside 1..%d", register, length, maximumCertificateBytes)
	}
	deadline := d.now().Add(d.ioRetryTimeout)
	var lastError error
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		selection, err := d.transport.Transaction(ctx, d.address, []byte{register}, 0)
		if err == nil && len(selection) != 0 {
			err = fmt.Errorf("register selection returned unexpected data")
		}
		if err == nil {
			var result []byte
			result, err = d.transport.Transaction(ctx, d.address, nil, length)
			if err == nil && len(result) != length {
				err = fmt.Errorf("register read returned %d bytes; expected %d", len(result), length)
			}
			if err == nil {
				return append([]byte(nil), result...), nil
			}
		}
		lastError = err
		if !d.now().Before(deadline) {
			return nil, fmt.Errorf("read register 0x%02x: %w", register, lastError)
		}
		if err := d.wait(ctx, d.ioRetryDelay); err != nil {
			return nil, err
		}
	}
}

func waitContext(ctx context.Context, delay time.Duration) error {
	if delay == 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
