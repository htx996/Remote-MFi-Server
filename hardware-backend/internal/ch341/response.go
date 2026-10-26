// SPDX-License-Identifier: GPL-3.0-only

package ch341

import "fmt"

const (
	lengthMask = 0x3f
	ackBit     = 0x80
)

// InstructionLengths reports the maximum possible leading status bytes and
// exact trailing data bytes produced by one encoded CH341 stream segment.
func InstructionLengths(segment []byte) (writtenStatusMax int, dataBytes int, err error) {
	if len(segment) == 0 || segment[0] != streamStart {
		return 0, 0, fmt.Errorf("CH341 stream segment must start with 0x%02x", streamStart)
	}
	for index := 1; index < len(segment); {
		command := int(segment[index])
		index++
		switch {
		case command == streamEnd:
			return writtenStatusMax, dataBytes, nil
		case command == streamI2CStart || command == streamI2CStop:
		case command >= 0x80 && command <= 0xbf:
			written := encodedLength(command)
			if command&lengthMask == 0 {
				writtenStatusMax += written
			}
			index += written
		case command >= 0xc0 && command <= 0xff:
			dataBytes += encodedLength(command)
		case command >= 0x60 && command <= 0x63:
		default:
			return 0, 0, fmt.Errorf("unexpected CH341 stream command 0x%02x", command)
		}
		if index > len(segment) {
			return 0, 0, fmt.Errorf("truncated CH341 stream command")
		}
	}
	return writtenStatusMax, dataBytes, nil
}

// ExtractReadData checks every optional leading ACK/NACK status byte and
// returns the requested data aligned from the tail of the USB response.
func ExtractReadData(received []byte, dataBytes int) ([]byte, error) {
	if dataBytes < 0 {
		return nil, fmt.Errorf("requested data length must not be negative")
	}
	if len(received) < dataBytes {
		return nil, fmt.Errorf("CH341 answered %d bytes; expected %d data bytes", len(received), dataBytes)
	}
	dataStart := len(received) - dataBytes
	for offset, status := range received[:dataStart] {
		if status&ackBit != 0 {
			return nil, fmt.Errorf("CH341 reported I2C NACK on byte %d", offset)
		}
	}
	return append([]byte(nil), received[dataStart:]...), nil
}

func encodedLength(command int) int {
	encoded := command & lengthMask
	if encoded == 0 {
		return 1
	}
	return encoded
}
