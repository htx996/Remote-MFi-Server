package mfi

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"
)

type transactionStep struct {
	address  uint8
	write    []byte
	readLen  int
	response []byte
	err      error
}

type scriptedTransport struct {
	testingT testing.TB
	steps    []transactionStep
	calls    []transactionStep
}

func newScriptedTransport(t testing.TB, steps ...transactionStep) *scriptedTransport {
	t.Helper()
	return &scriptedTransport{testingT: t, steps: append([]transactionStep(nil), steps...)}
}

func (t *scriptedTransport) Transaction(
	_ context.Context,
	address uint8,
	writeData []byte,
	readLength int,
) ([]byte, error) {
	t.testingT.Helper()
	call := transactionStep{address: address, write: append([]byte(nil), writeData...), readLen: readLength}
	t.calls = append(t.calls, call)
	if len(t.steps) == 0 {
		t.testingT.Fatalf("unexpected transaction: address=0x%02x write=%x read=%d", address, writeData, readLength)
	}
	step := t.steps[0]
	t.steps = t.steps[1:]
	if address != step.address || !bytes.Equal(writeData, step.write) || readLength != step.readLen {
		t.testingT.Fatalf(
			"transaction = address 0x%02x write %x read %d, want address 0x%02x write %x read %d",
			address, writeData, readLength, step.address, step.write, step.readLen,
		)
	}
	return append([]byte(nil), step.response...), step.err
}

func (t *scriptedTransport) Close() error { return nil }

func (t *scriptedTransport) assertConsumed() {
	t.testingT.Helper()
	if len(t.steps) != 0 {
		t.testingT.Fatalf("%d scripted transactions remain", len(t.steps))
	}
}

func selectRegister(address, register uint8) transactionStep {
	return transactionStep{address: address, write: []byte{register}}
}

func readBytes(address uint8, response ...byte) transactionStep {
	return transactionStep{address: address, readLen: len(response), response: append([]byte(nil), response...)}
}

func TestProbeReadsRequiredRegistersUsingSeparateTransactions(t *testing.T) {
	transport := newScriptedTransport(t,
		selectRegister(0x11, 0x00), readBytes(0x11, 0x03),
		selectRegister(0x11, 0x02), readBytes(0x11, 0x03),
		selectRegister(0x11, 0x30), readBytes(0x11, 0x02, 0x5f),
	)
	driver, err := NewDriver(transport, 0x11)
	if err != nil {
		t.Fatalf("NewDriver() error = %v", err)
	}

	got, err := driver.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	want := ProbeResult{Address: 0x11, DeviceVersion: 0x03, ProtocolMajor: 0x03, CertificateLength: 607}
	if got != want {
		t.Fatalf("Probe() = %#v, want %#v", got, want)
	}
	transport.assertConsumed()
}

func TestProbeRetriesTheWholeRegisterSelectReadPair(t *testing.T) {
	transport := newScriptedTransport(t,
		transactionStep{address: 0x11, write: []byte{0x00}, err: fmt.Errorf("temporary I2C NACK")},
		selectRegister(0x11, 0x00), readBytes(0x11, 0x03),
		selectRegister(0x11, 0x02), readBytes(0x11, 0x03),
		selectRegister(0x11, 0x30), readBytes(0x11, 0x02, 0x5f),
	)
	driver, err := NewDriver(transport, 0x11, WithRetryTiming(time.Second, 0))
	if err != nil {
		t.Fatalf("NewDriver() error = %v", err)
	}

	if _, err := driver.Probe(context.Background()); err != nil {
		t.Fatalf("Probe() did not retry the entire select/read pair: %v", err)
	}
	transport.assertConsumed()
}

func TestConfiguredAddressNeverFallsBackToAnswering0x10(t *testing.T) {
	transport := newScriptedTransport(t,
		selectRegister(0x11, 0x00), readBytes(0x11, 0x03),
		selectRegister(0x11, 0x02), readBytes(0x11, 0x03),
		selectRegister(0x11, 0x30), readBytes(0x11, 0x00, 0x00),
	)
	driver, err := NewDriver(transport, 0x11)
	if err != nil {
		t.Fatalf("NewDriver() error = %v", err)
	}

	if _, err := driver.Probe(context.Background()); err == nil {
		t.Fatal("Probe() accepted invalid configured address and could silently fall back")
	}
	for _, call := range transport.calls {
		if call.address != 0x11 {
			t.Fatalf("Probe() used address 0x%02x, want configured 0x11 only", call.address)
		}
	}
	transport.assertConsumed()
}

func TestProbeRejectsInvalidDeviceVersions(t *testing.T) {
	for _, version := range []byte{0x00, 0xff} {
		t.Run(fmt.Sprintf("0x%02x", version), func(t *testing.T) {
			transport := newScriptedTransport(t,
				selectRegister(0x11, 0x00), readBytes(0x11, version),
			)
			driver, err := NewDriver(transport, 0x11)
			if err != nil {
				t.Fatalf("NewDriver() error = %v", err)
			}
			if _, err := driver.Probe(context.Background()); err == nil {
				t.Fatalf("Probe() accepted invalid device version 0x%02x", version)
			}
			transport.assertConsumed()
		})
	}
}

func TestProbeRejectsInvalidProtocolMajor(t *testing.T) {
	for _, value := range []byte{0x00, 0xff} {
		t.Run(fmt.Sprintf("0x%02x", value), func(t *testing.T) {
			transport := newScriptedTransport(t,
				selectRegister(0x11, 0x00), readBytes(0x11, 0x03),
				selectRegister(0x11, 0x02), readBytes(0x11, value),
			)
			driver, err := NewDriver(transport, 0x11)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := driver.Probe(context.Background()); err == nil {
				t.Fatal("invalid protocol major accepted")
			}
		})
	}
}

func TestProbeRejectsCertificateLengthOutsideMFiLimit(t *testing.T) {
	for _, length := range []uint16{0, 1281} {
		t.Run(fmt.Sprintf("%d", length), func(t *testing.T) {
			transport := newScriptedTransport(t,
				selectRegister(0x11, 0x00), readBytes(0x11, 0x03),
				selectRegister(0x11, 0x02), readBytes(0x11, 0x03),
				selectRegister(0x11, 0x30), readBytes(0x11, byte(length>>8), byte(length)),
			)
			driver, err := NewDriver(transport, 0x11)
			if err != nil {
				t.Fatalf("NewDriver() error = %v", err)
			}
			if _, err := driver.Probe(context.Background()); err == nil {
				t.Fatalf("Probe() accepted certificate length %d", length)
			}
			transport.assertConsumed()
		})
	}
}
