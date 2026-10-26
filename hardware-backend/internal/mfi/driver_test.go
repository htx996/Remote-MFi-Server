package mfi

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReadCertificateUsesFiveBoundedWindowsFor607Bytes(t *testing.T) {
	certificate := make([]byte, 607)
	for index := range certificate {
		certificate[index] = byte(index * 37)
	}
	transport := newScriptedTransport(t,
		selectRegister(0x11, 0x30), readBytes(0x11, 0x02, 0x5f),
		selectRegister(0x11, 0x31), transactionStep{address: 0x11, readLen: 128, response: certificate[:128]},
		selectRegister(0x11, 0x32), transactionStep{address: 0x11, readLen: 128, response: certificate[128:256]},
		selectRegister(0x11, 0x33), transactionStep{address: 0x11, readLen: 128, response: certificate[256:384]},
		selectRegister(0x11, 0x34), transactionStep{address: 0x11, readLen: 128, response: certificate[384:512]},
		selectRegister(0x11, 0x35), transactionStep{address: 0x11, readLen: 95, response: certificate[512:]},
	)
	driver, err := NewDriver(transport, 0x11)
	if err != nil {
		t.Fatalf("NewDriver() error = %v", err)
	}

	got, err := driver.ReadCertificate(context.Background())
	if err != nil {
		t.Fatalf("ReadCertificate() error = %v", err)
	}
	if !bytes.Equal(got, certificate) {
		t.Fatal("ReadCertificate() returned bytes different from the five hardware windows")
	}
	transport.assertConsumed()
}

func TestReadCertificateErrorDoesNotContainCertificateBytes(t *testing.T) {
	secretWindow := bytes.Repeat([]byte("S"), 128)
	transport := newScriptedTransport(t,
		selectRegister(0x11, 0x30), readBytes(0x11, 0x00, 0x81),
		selectRegister(0x11, 0x31), transactionStep{address: 0x11, readLen: 128, response: secretWindow},
		transactionStep{address: 0x11, write: []byte{0x32}, err: errors.New("USB transfer failed")},
	)
	driver, err := NewDriver(transport, 0x11, WithRetryTiming(0, 0))
	if err != nil {
		t.Fatalf("NewDriver() error = %v", err)
	}

	_, err = driver.ReadCertificate(context.Background())
	if err == nil {
		t.Fatal("ReadCertificate() succeeded after hardware failure")
	}
	message := err.Error()
	if strings.Contains(message, strings.Repeat("S", 16)) ||
		strings.Contains(message, base64.StdEncoding.EncodeToString(secretWindow)) {
		t.Fatalf("ReadCertificate() error exposed certificate bytes: %q", message)
	}
	transport.assertConsumed()
}

func writeRegister(address, register byte, payload []byte) transactionStep {
	request := append([]byte{register}, payload...)
	return transactionStep{address: address, write: request}
}

func TestSignChallengeRejectsInvalidLengthsBeforeTransportAccess(t *testing.T) {
	for _, challenge := range [][]byte{nil, make([]byte, 129)} {
		transport := newScriptedTransport(t)
		driver, err := NewDriver(transport, 0x11)
		if err != nil {
			t.Fatalf("NewDriver() error = %v", err)
		}
		if _, err := driver.SignChallenge(context.Background(), challenge); err == nil {
			t.Fatalf("SignChallenge() accepted %d-byte challenge", len(challenge))
		}
		if len(transport.calls) != 0 {
			t.Fatalf("SignChallenge() accessed transport for %d-byte challenge", len(challenge))
		}
	}
}

func TestSignChallengeUsesDocumentedRegistersAndWaits(t *testing.T) {
	challenge := make([]byte, 32)
	for index := range challenge {
		challenge[index] = byte(index + 1)
	}
	signature := bytes.Repeat([]byte{0xa5}, 64)
	transport := newScriptedTransport(t,
		writeRegister(0x11, 0x20, []byte{0x00, 0x20}),
		writeRegister(0x11, 0x21, challenge),
		writeRegister(0x11, 0x10, []byte{0x01}),
		selectRegister(0x11, 0x10), readBytes(0x11, 0x11),
		selectRegister(0x11, 0x10), readBytes(0x11, 0x10),
		selectRegister(0x11, 0x11), readBytes(0x11, 0x00, 0x40),
		selectRegister(0x11, 0x12), transactionStep{address: 0x11, readLen: 64, response: signature},
	)
	var waits []time.Duration
	driver, err := NewDriver(
		transport,
		0x11,
		WithAuthenticationTiming(7*time.Millisecond, 11*time.Millisecond, time.Second),
		func(driver *Driver) error {
			driver.wait = func(ctx context.Context, delay time.Duration) error {
				waits = append(waits, delay)
				return ctx.Err()
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("NewDriver() error = %v", err)
	}

	got, err := driver.SignChallenge(context.Background(), challenge)
	if err != nil {
		t.Fatalf("SignChallenge() error = %v", err)
	}
	if !bytes.Equal(got, signature) {
		t.Fatal("SignChallenge() did not return the signature data register bytes")
	}
	wantWaits := []time.Duration{7 * time.Millisecond, 11 * time.Millisecond}
	if len(waits) != len(wantWaits) || waits[0] != wantWaits[0] || waits[1] != wantWaits[1] {
		t.Fatalf("SignChallenge() waits = %v, want %v", waits, wantWaits)
	}
	transport.assertConsumed()
}

type registerTransport struct {
	selected        byte
	calls           []transactionStep
	status          byte
	errorCode       byte
	statusReadCount int
}

func (t *registerTransport) Transaction(
	_ context.Context,
	address uint8,
	writeData []byte,
	readLength int,
) ([]byte, error) {
	t.calls = append(t.calls, transactionStep{address: address, write: append([]byte(nil), writeData...), readLen: readLength})
	if address != 0x11 {
		return nil, errors.New("unexpected address")
	}
	if readLength == 0 {
		if len(writeData) == 1 {
			t.selected = writeData[0]
		}
		return nil, nil
	}
	switch t.selected {
	case 0x10:
		t.statusReadCount++
		return []byte{t.status}, nil
	case 0x05:
		return []byte{t.errorCode}, nil
	default:
		return nil, errors.New("unexpected read register")
	}
}

func (t *registerTransport) Close() error { return nil }

type fakeClock struct {
	now   time.Time
	waits []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Wait(ctx context.Context, delay time.Duration) error {
	c.waits = append(c.waits, delay)
	c.now = c.now.Add(delay)
	return ctx.Err()
}

func withFakeClock(clock *fakeClock) Option {
	return func(driver *Driver) error {
		driver.now = clock.Now
		driver.wait = clock.Wait
		return nil
	}
}

func TestSignChallengeTimesOutAfterThreeSecondsAndReadsErrorRegister(t *testing.T) {
	transport := &registerTransport{status: 0x01, errorCode: 0x42}
	clock := &fakeClock{now: time.Unix(1, 0)}
	driver, err := NewDriver(transport, 0x11, withFakeClock(clock))
	if err != nil {
		t.Fatalf("NewDriver() error = %v", err)
	}

	_, err = driver.SignChallenge(context.Background(), []byte{0x01})
	if err == nil {
		t.Fatal("SignChallenge() succeeded without authentication status 0x10")
	}
	if !strings.Contains(err.Error(), "0x42") {
		t.Fatalf("SignChallenge() error = %q, want best-effort MFi error 0x42", err)
	}
	if elapsed := clock.now.Sub(time.Unix(1, 0)); elapsed < 3*time.Second || elapsed > 3*time.Second+10*time.Millisecond {
		t.Fatalf("authentication timeout elapsed = %s, want 3s plus at most one poll", elapsed)
	}
	if len(clock.waits) == 0 || clock.waits[0] != 10*time.Millisecond {
		t.Fatalf("initial wait = %v, want 10ms", clock.waits)
	}
	foundErrorRegister := false
	for _, call := range transport.calls {
		if bytes.Equal(call.write, []byte{0x05}) {
			foundErrorRegister = true
		}
	}
	if !foundErrorRegister {
		t.Fatal("SignChallenge() did not read best-effort error register 0x05")
	}
}

func TestSignChallengeCancellationStopsPolling(t *testing.T) {
	transport := &registerTransport{status: 0x01}
	ctx, cancel := context.WithCancel(context.Background())
	waitCount := 0
	driver, err := NewDriver(transport, 0x11, func(driver *Driver) error {
		driver.wait = func(ctx context.Context, _ time.Duration) error {
			waitCount++
			if waitCount == 2 {
				cancel()
			}
			return ctx.Err()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewDriver() error = %v", err)
	}

	_, err = driver.SignChallenge(ctx, []byte{0x01})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SignChallenge() error = %v, want context canceled", err)
	}
	if transport.statusReadCount != 1 {
		t.Fatalf("status reads after cancellation = %d, want 1", transport.statusReadCount)
	}
}
