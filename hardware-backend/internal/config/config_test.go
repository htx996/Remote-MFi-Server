package config

import "testing"

func lookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func TestFromEnvironmentUsesBoardDefaults(t *testing.T) {
	got, err := FromEnvironment(lookup(nil))
	if err != nil {
		t.Fatalf("FromEnvironment() error = %v", err)
	}
	want := Config{
		USBVendor:   0x1a86,
		USBProduct:  0x5512,
		I2CAddress:  0x11,
		I2CSpeedKHz: 100,
	}
	if got != want {
		t.Fatalf("FromEnvironment() = %#v, want %#v", got, want)
	}
}

func TestFromEnvironmentAcceptsExplicitHexValues(t *testing.T) {
	got, err := FromEnvironment(lookup(map[string]string{
		"CH341_USB_VID":       "0x1A86",
		"CH341_USB_PID":       "0x5512",
		"MFI_I2C_ADDRESS":     "0x11",
		"CH341_I2C_SPEED_KHZ": "0x190",
	}))
	if err != nil {
		t.Fatalf("FromEnvironment() error = %v", err)
	}
	want := Config{USBVendor: 0x1a86, USBProduct: 0x5512, I2CAddress: 0x11, I2CSpeedKHz: 400}
	if got != want {
		t.Fatalf("FromEnvironment() = %#v, want %#v", got, want)
	}
}

func TestFromEnvironmentRejectsOutOfRangeAddressBeforeHardwareAccess(t *testing.T) {
	for _, value := range []string{"-1", "0x80"} {
		t.Run(value, func(t *testing.T) {
			_, err := FromEnvironment(lookup(map[string]string{"MFI_I2C_ADDRESS": value}))
			if err == nil {
				t.Fatalf("FromEnvironment() accepted out-of-range I2C address %q", value)
			}
		})
	}
}

func TestFromEnvironmentRejectsUnsupportedSpeed(t *testing.T) {
	for _, value := range []string{"0", "19", "21", "101", "1000"} {
		t.Run(value, func(t *testing.T) {
			_, err := FromEnvironment(lookup(map[string]string{"CH341_I2C_SPEED_KHZ": value}))
			if err == nil {
				t.Fatalf("FromEnvironment() accepted unsupported I2C speed %q", value)
			}
		})
	}
}

func TestFromEnvironmentRejectsMalformedVIDPID(t *testing.T) {
	tests := []map[string]string{
		{"CH341_USB_VID": "wch"},
		{"CH341_USB_VID": "0x10000"},
		{"CH341_USB_PID": "-1"},
		{"CH341_USB_PID": "0x10000"},
	}
	for _, values := range tests {
		_, err := FromEnvironment(lookup(values))
		if err == nil {
			t.Fatalf("FromEnvironment() accepted malformed USB ID %#v", values)
		}
	}
}
