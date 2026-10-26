// Package config validates the non-secret hardware settings used by the
// CH341F/MFi helper before any USB device is opened.
package config

import (
	"fmt"
	"strconv"
)

const (
	defaultUSBVendor   = 0x1a86
	defaultUSBProduct  = 0x5512
	defaultI2CAddress  = 0x11
	defaultI2CSpeedKHz = 100
)

// Config identifies the CH341F and the MFi I2C bus settings.
type Config struct {
	USBVendor   uint16
	USBProduct  uint16
	I2CAddress  uint8
	I2CSpeedKHz int
}

// FromEnvironment parses hardware settings using lookup. Integer values use
// Go syntax, so both decimal and 0x-prefixed hexadecimal forms are accepted.
func FromEnvironment(lookup func(string) (string, bool)) (Config, error) {
	if lookup == nil {
		return Config{}, fmt.Errorf("environment lookup is required")
	}

	vendor, err := unsignedSetting(lookup, "CH341_USB_VID", defaultUSBVendor, 16)
	if err != nil {
		return Config{}, err
	}
	product, err := unsignedSetting(lookup, "CH341_USB_PID", defaultUSBProduct, 16)
	if err != nil {
		return Config{}, err
	}
	address, err := unsignedSetting(lookup, "MFI_I2C_ADDRESS", defaultI2CAddress, 7)
	if err != nil {
		return Config{}, err
	}
	speed, err := speedSetting(lookup)
	if err != nil {
		return Config{}, err
	}

	return Config{
		USBVendor:   uint16(vendor),
		USBProduct:  uint16(product),
		I2CAddress:  uint8(address),
		I2CSpeedKHz: speed,
	}, nil
}

func unsignedSetting(
	lookup func(string) (string, bool),
	name string,
	fallback uint64,
	bits int,
) (uint64, error) {
	value, ok := lookup(name)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(value, 0, bits)
	if err != nil {
		return 0, fmt.Errorf("%s must be an unsigned %d-bit integer: %w", name, bits, err)
	}
	return parsed, nil
}

func speedSetting(lookup func(string) (string, bool)) (int, error) {
	value, ok := lookup("CH341_I2C_SPEED_KHZ")
	if !ok {
		return defaultI2CSpeedKHz, nil
	}
	parsed, err := strconv.ParseInt(value, 0, 32)
	if err != nil {
		return 0, fmt.Errorf("CH341_I2C_SPEED_KHZ must be an integer: %w", err)
	}
	speed := int(parsed)
	switch speed {
	case 20, 100, 400, 750:
		return speed, nil
	default:
		return 0, fmt.Errorf("CH341_I2C_SPEED_KHZ must be one of 20, 100, 400, or 750")
	}
}
