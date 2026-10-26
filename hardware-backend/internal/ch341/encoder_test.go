package ch341

import (
	"bytes"
	"testing"
)

func TestEncodeConfiguration100KHz(t *testing.T) {
	got, err := EncodeConfiguration(100)
	if err != nil {
		t.Fatalf("EncodeConfiguration() error = %v", err)
	}
	want := []byte{0xaa, 0x61, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("EncodeConfiguration() = % x, want % x", got, want)
	}
}

func TestEncodeWriteOnlyRegisterSelectionEndsWithStop(t *testing.T) {
	got, err := EncodeTransaction(0x11, []byte{0x30}, 0)
	if err != nil {
		t.Fatalf("EncodeTransaction() error = %v", err)
	}
	want := []byte{0xaa, 0x74, 0x80, 0x22, 0x80, 0x30, 0x75, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("EncodeTransaction() = % x, want % x", got, want)
	}
}

func TestEncodePureOneByteRead(t *testing.T) {
	got, err := EncodeTransaction(0x11, nil, 1)
	if err != nil {
		t.Fatalf("EncodeTransaction() error = %v", err)
	}
	want := []byte{0xaa, 0x74, 0x80, 0x23, 0xc0, 0x75, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("EncodeTransaction() = % x, want % x", got, want)
	}
}

func TestEncodeRejectsCombinedWriteRead(t *testing.T) {
	if _, err := EncodeTransaction(0x11, []byte{0x30}, 2); err == nil {
		t.Fatal("EncodeTransaction() accepted combined write/read transaction")
	}
}

func TestEncodeMaximumMFiPayloadUses32ByteSegments(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5a}, 128)
	got, err := EncodeTransaction(0x11, payload, 0)
	if err != nil {
		t.Fatalf("EncodeTransaction() error = %v", err)
	}
	if len(got) != 279 {
		t.Fatalf("encoded length = %d, want 279", len(got))
	}
	first := append([]byte{0xaa, 0x74, 0x80, 0x22}, bytes.Repeat([]byte{0x80, 0x5a}, 13)...)
	first = append(first, 0x00, 0x00)
	if !bytes.Equal(got[:32], first) {
		t.Fatalf("first segment = % x, want % x", got[:32], first)
	}
	middle := append([]byte{0xaa}, bytes.Repeat([]byte{0x80, 0x5a}, 15)...)
	middle = append(middle, 0x00)
	for offset := 32; offset < 8*32; offset += 32 {
		if !bytes.Equal(got[offset:offset+32], middle) {
			t.Fatalf("segment at %d = % x, want % x", offset, got[offset:offset+32], middle)
		}
	}
	last := append([]byte{0xaa}, bytes.Repeat([]byte{0x80, 0x5a}, 10)...)
	last = append(last, 0x75, 0x00)
	if !bytes.Equal(got[8*32:], last) {
		t.Fatalf("final segment = % x, want % x", got[8*32:], last)
	}
	if bytes.Count(got, []byte{0x75}) != 1 {
		t.Fatalf("STOP count = %d, want 1", bytes.Count(got, []byte{0x75}))
	}
}

func TestEncodeRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name      string
		address   uint8
		writeData []byte
		readLen   int
	}{
		{name: "empty", address: 0x11},
		{name: "address", address: 0x80, writeData: []byte{1}},
		{name: "negative read", address: 0x11, readLen: -1},
		{name: "oversize read", address: 0x11, readLen: 0x10000},
		{name: "oversize write", address: 0x11, writeData: make([]byte, 0x10000)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := EncodeTransaction(test.address, test.writeData, test.readLen); err == nil {
				t.Fatal("EncodeTransaction() accepted invalid request")
			}
		})
	}
}
