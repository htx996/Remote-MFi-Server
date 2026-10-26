package ch341

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeRead struct {
	data []byte
	err  error
}

type fakeBulkSession struct {
	writes        [][]byte
	reads         []fakeRead
	readCalls     int
	closed        bool
	writeOverride func(context.Context, []byte) (int, error)
}

func (s *fakeBulkSession) Write(ctx context.Context, data []byte) (int, error) {
	s.writes = append(s.writes, append([]byte(nil), data...))
	if s.writeOverride != nil {
		return s.writeOverride(ctx, data)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (s *fakeBulkSession) Read(ctx context.Context, buffer []byte) (int, error) {
	s.readCalls++
	if len(s.reads) == 0 {
		return 0, context.DeadlineExceeded
	}
	read := s.reads[0]
	s.reads = s.reads[1:]
	if read.err != nil {
		return 0, read.err
	}
	return copy(buffer, read.data), nil
}

func TestTransportReadsSegmentedCertificateWindows(t *testing.T) {
	for _, length := range []int{33, 64, 95, 128} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			want := make([]byte, length)
			for index := range want {
				want[index] = byte(index)
			}
			session := &fakeBulkSession{}
			for offset := 0; offset < length; offset += 32 {
				end := min(offset+32, length)
				session.reads = append(session.reads, fakeRead{data: want[offset:end]})
			}
			transport, err := newTransport(session, 100, func(transport *Transport) {
				transport.wait = func(context.Context, time.Duration) error { return nil }
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := transport.Transaction(context.Background(), 0x11, nil, length)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("read %d bytes: error=%v got length=%d", length, err, len(got))
			}
			if session.readCalls != (length+31)/32 {
				t.Fatalf("read calls=%d, want %d", session.readCalls, (length+31)/32)
			}
		})
	}
}

func (s *fakeBulkSession) Close() error {
	s.closed = true
	return nil
}

func TestTransportConfiguresBeforeEveryTransaction(t *testing.T) {
	session := &fakeBulkSession{}
	transport, err := newTransport(session, 100)
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	if _, err := transport.Transaction(context.Background(), 0x11, []byte{0x00}, 0); err != nil {
		t.Fatalf("first Transaction() error = %v", err)
	}
	if _, err := transport.Transaction(context.Background(), 0x11, []byte{0x02}, 0); err != nil {
		t.Fatalf("second Transaction() error = %v", err)
	}
	wantConfig := []byte{0xaa, 0x61, 0x00}
	if len(session.writes) != 4 {
		t.Fatalf("USB writes = %d, want config+stream twice", len(session.writes))
	}
	if !bytes.Equal(session.writes[0], wantConfig) || !bytes.Equal(session.writes[2], wantConfig) {
		t.Fatalf("configuration writes = % x / % x, want % x", session.writes[0], session.writes[2], wantConfig)
	}
}

func TestTransportPureReadWaitsAtLeastFiveMilliseconds(t *testing.T) {
	session := &fakeBulkSession{reads: []fakeRead{{data: []byte{0x00, 0xab}}}}
	var waits []time.Duration
	transport, err := newTransport(session, 100, func(transport *Transport) {
		transport.wait = func(ctx context.Context, delay time.Duration) error {
			waits = append(waits, delay)
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	got, err := transport.Transaction(context.Background(), 0x11, nil, 1)
	if err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}
	if !bytes.Equal(got, []byte{0xab}) {
		t.Fatalf("Transaction() = % x, want ab", got)
	}
	if len(waits) != 1 || waits[0] < 5*time.Millisecond {
		t.Fatalf("pure-read waits = %v, want one wait >= 5ms", waits)
	}
}

func TestTransportSplitsLargeTransferInto32ByteUSBWrites(t *testing.T) {
	session := &fakeBulkSession{}
	transport, err := newTransport(session, 100)
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	if _, err := transport.Transaction(context.Background(), 0x11, bytes.Repeat([]byte{0x5a}, 128), 0); err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}
	if len(session.writes) != 10 {
		t.Fatalf("USB writes = %d, want one config plus nine stream segments", len(session.writes))
	}
	for index, write := range session.writes[1:] {
		if len(write) > 32 {
			t.Fatalf("stream write %d length = %d, exceeds 32", index, len(write))
		}
	}
}

func TestTransportAllowsWriteOnlyPacketWithNoResponse(t *testing.T) {
	session := &fakeBulkSession{reads: []fakeRead{{err: context.DeadlineExceeded}}}
	transport, err := newTransport(session, 100)
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	got, err := transport.Transaction(context.Background(), 0x11, []byte{0x30}, 0)
	if err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Transaction() returned write-only data % x", got)
	}
}

func TestTransportReadRequiresEveryRequestedDataByte(t *testing.T) {
	session := &fakeBulkSession{reads: []fakeRead{
		{data: []byte{0x00}},
		{err: context.DeadlineExceeded},
	}}
	transport, err := newTransport(session, 100, func(transport *Transport) {
		transport.wait = func(context.Context, time.Duration) error { return nil }
	})
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	if _, err := transport.Transaction(context.Background(), 0x11, nil, 2); err == nil {
		t.Fatal("Transaction() accepted a response shorter than the requested data")
	}
}

func TestTransportCancellationOrTimeoutClosesSession(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			session := &fakeBulkSession{writeOverride: func(context.Context, []byte) (int, error) {
				return 0, cause
			}}
			transport, err := newTransport(session, 100)
			if err != nil {
				t.Fatalf("newTransport() error = %v", err)
			}
			if _, err := transport.Transaction(context.Background(), 0x11, []byte{0x00}, 0); !errors.Is(err, cause) {
				t.Fatalf("Transaction() error = %v, want %v", err, cause)
			}
			if !session.closed {
				t.Fatal("timed-out/cancelled transaction did not close USB session")
			}
		})
	}
}

func TestTransportClaimErrorsAreSafeAndExcludePayloadBytes(t *testing.T) {
	secret := "deadbeef-certificate-challenge-signature"
	for _, sentinel := range []error{ErrUSBPermission, ErrUSBBusy} {
		raw := fmt.Errorf("%s: %w", secret, sentinel)
		got := safeUSBError("claim CH341 interface", raw).Error()
		if strings.Contains(got, secret) {
			t.Fatalf("safeUSBError() exposed payload bytes: %q", got)
		}
	}
}
