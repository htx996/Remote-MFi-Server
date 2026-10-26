package ch341

import (
	"bytes"
	"testing"
)

func TestInstructionLengthsCountsBareWritesAsPossibleStatusBytes(t *testing.T) {
	segment := []byte{0xaa, 0x74, 0x80, 0x22, 0x80, 0x30, 0x75, 0x00}
	written, data, err := InstructionLengths(segment)
	if err != nil {
		t.Fatalf("InstructionLengths() error = %v", err)
	}
	if written != 2 || data != 0 {
		t.Fatalf("InstructionLengths() = (%d, %d), want (2, 0)", written, data)
	}
}

func TestInstructionLengthsBatchedWriteDoesNotRequireStatusBytes(t *testing.T) {
	segment := []byte{0xaa, 0x74, 0x81, 0x23, 0xdf, 0xc0, 0x75, 0x00}
	written, data, err := InstructionLengths(segment)
	if err != nil {
		t.Fatalf("InstructionLengths() error = %v", err)
	}
	if written != 0 || data != 32 {
		t.Fatalf("InstructionLengths() = (%d, %d), want (0, 32)", written, data)
	}
}

func TestExtractReadDataAlignsDataFromResponseTail(t *testing.T) {
	got, err := ExtractReadData([]byte{0x00, 0x00, 0xde, 0xad, 0xbe, 0xef}, 4)
	if err != nil {
		t.Fatalf("ExtractReadData() error = %v", err)
	}
	want := []byte{0xde, 0xad, 0xbe, 0xef}
	if !bytes.Equal(got, want) {
		t.Fatalf("ExtractReadData() = % x, want % x", got, want)
	}
}

func TestExtractReadDataRejectsLeadingNACK(t *testing.T) {
	for _, response := range [][]byte{
		{0x80, 0x01, 0x02},
		{0x00, 0xff, 0x01, 0x02},
	} {
		if _, err := ExtractReadData(response, 2); err == nil {
			t.Fatalf("ExtractReadData() accepted NACK response % x", response)
		}
	}
}

func TestExtractReadDataRejectsShortResponse(t *testing.T) {
	if _, err := ExtractReadData([]byte{0x01}, 2); err == nil {
		t.Fatal("ExtractReadData() accepted response shorter than requested data")
	}
}

func TestExtractReadDataAcceptsExactlyOneFullUSBPacket(t *testing.T) {
	response := bytes.Repeat([]byte{0x5a}, 32)
	got, err := ExtractReadData(response, 32)
	if err != nil {
		t.Fatalf("ExtractReadData() error = %v", err)
	}
	if !bytes.Equal(got, response) {
		t.Fatalf("ExtractReadData() = % x, want one exact 32-byte packet", got)
	}
}
