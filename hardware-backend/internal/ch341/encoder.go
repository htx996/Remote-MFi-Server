// SPDX-License-Identifier: GPL-3.0-only

// Package ch341 encodes the CH341F USB stream protocol used for I2C access.
package ch341

import "fmt"

const (
	maxStreamPacketBytes    = 32
	maxTransactionBytes     = 0xffff
	maxReadBlockBytes       = 32
	maxWriteCommandBytes    = 0x3f
	streamStart             = 0xaa
	streamEnd               = 0x00
	streamI2CStart          = 0x74
	streamI2CStop           = 0x75
	streamWrite             = 0x80
	streamRead              = 0xc0
	streamEndBytes          = 1
	streamStopAndEndBytes   = 2
	streamStartAddressBytes = 3
)

// EncodeConfiguration returns the CH341 stream command for an I2C clock.
func EncodeConfiguration(speedKHz int) ([]byte, error) {
	var command byte
	switch speedKHz {
	case 20:
		command = 0x60
	case 100:
		command = 0x61
	case 400:
		command = 0x62
	case 750:
		command = 0x63
	default:
		return nil, fmt.Errorf("unsupported CH341 I2C speed %d kHz", speedKHz)
	}
	return []byte{streamStart, command, streamEnd}, nil
}

// EncodeTransaction returns one or more CH341 stream packets. MFi register
// reads use separate write-only register selection and pure-read transactions,
// so combined write/read requests are deliberately rejected.
func EncodeTransaction(address uint8, writeData []byte, readLength int) ([]byte, error) {
	if address > 0x7f {
		return nil, fmt.Errorf("I2C address must be a 7-bit value")
	}
	if readLength < 0 {
		return nil, fmt.Errorf("read length must not be negative")
	}
	if len(writeData) == 0 && readLength == 0 {
		return nil, fmt.Errorf("transaction must read or write data")
	}
	if len(writeData) > 0 && readLength > 0 {
		return nil, fmt.Errorf("combined write/read transaction is not supported")
	}
	if len(writeData) > maxTransactionBytes || readLength > maxTransactionBytes {
		return nil, fmt.Errorf("transaction supports at most %d bytes per read or write", maxTransactionBytes)
	}
	if legacy := encodeLegacyTransaction(address, writeData, readLength); legacy != nil {
		return legacy, nil
	}

	stream := newSegmentedStream()
	if len(writeData) > 0 {
		stream.startAndWrite(addressByte(address, false), writeData)
	} else {
		stream.startAndWrite(addressByte(address, true), nil)
		stream.read(readLength)
	}
	return stream.finish(), nil
}

func encodeLegacyTransaction(address uint8, writeData []byte, readLength int) []byte {
	if readLength > maxReadBlockBytes || len(writeData)+1 > maxWriteCommandBytes {
		return nil
	}

	encoded := make([]byte, 0, maxStreamPacketBytes)
	write := func(addressByte byte, data []byte) {
		encoded = append(encoded, streamWrite, addressByte)
		for _, value := range data {
			encoded = append(encoded, streamWrite, value)
		}
	}

	encoded = append(encoded, streamStart, streamI2CStart)
	if len(writeData) > 0 {
		write(addressByte(address, false), writeData)
	} else {
		write(addressByte(address, true), nil)
		if readLength > 1 {
			encoded = append(encoded, byte(streamRead+readLength-1))
		}
		encoded = append(encoded, streamRead)
	}
	encoded = append(encoded, streamI2CStop, streamEnd)
	if len(encoded) > maxStreamPacketBytes {
		return nil
	}
	return encoded
}

func addressByte(address uint8, read bool) byte {
	value := address << 1
	if read {
		value |= 1
	}
	return value
}

type segmentedStream struct {
	result  []byte
	segment []byte
}

func newSegmentedStream() *segmentedStream {
	return &segmentedStream{segment: []byte{streamStart}}
}

func (s *segmentedStream) startAndWrite(address byte, data []byte) {
	if len(s.segment)+streamStartAddressBytes+streamEndBytes > maxStreamPacketBytes {
		s.finishIntermediate()
	}
	s.segment = append(s.segment, streamI2CStart)
	s.write(address, data)
}

func (s *segmentedStream) write(address byte, data []byte) {
	if len(data) == 0 {
		s.command([]byte{streamWrite + 1, address})
		return
	}
	s.command([]byte{streamWrite, address})
	for _, value := range data {
		s.command([]byte{streamWrite, value})
	}
}

func (s *segmentedStream) read(length int) {
	remaining := length
	for remaining > maxReadBlockBytes {
		s.command([]byte{streamRead + maxReadBlockBytes})
		s.finishIntermediate()
		remaining -= maxReadBlockBytes
	}
	if remaining > 1 {
		s.command([]byte{byte(streamRead + remaining - 1)})
	}
	s.command([]byte{streamRead})
}

func (s *segmentedStream) command(command []byte) {
	if len(s.segment)+len(command)+streamEndBytes > maxStreamPacketBytes {
		s.finishIntermediate()
	}
	s.segment = append(s.segment, command...)
}

func (s *segmentedStream) finishIntermediate() {
	s.segment = append(s.segment, streamEnd)
	for len(s.segment) < maxStreamPacketBytes {
		s.segment = append(s.segment, 0)
	}
	s.result = append(s.result, s.segment...)
	s.segment = []byte{streamStart}
}

func (s *segmentedStream) finish() []byte {
	if len(s.segment)+streamStopAndEndBytes > maxStreamPacketBytes {
		s.finishIntermediate()
	}
	s.segment = append(s.segment, streamI2CStop, streamEnd)
	s.result = append(s.result, s.segment...)
	return s.result
}
