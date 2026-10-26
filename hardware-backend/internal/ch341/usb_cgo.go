// SPDX-License-Identifier: GPL-3.0-only

//go:build cgo

package ch341

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/gousb"

	"github.com/htx996/remote-mfi-server/hardware-backend/internal/config"
	"github.com/htx996/remote-mfi-server/hardware-backend/internal/mfi"
)

type gousbSession struct {
	usbContext *gousb.Context
	device     *gousb.Device
	config     *gousb.Config
	iface      *gousb.Interface
	input      *gousb.InEndpoint
	output     *gousb.OutEndpoint
}

func (s *gousbSession) Write(ctx context.Context, data []byte) (int, error) {
	written, err := s.output.WriteContext(ctx, data)
	return written, transferError(ctx, err)
}

func (s *gousbSession) Read(ctx context.Context, data []byte) (int, error) {
	read, err := s.input.ReadContext(ctx, data)
	return read, transferError(ctx, err)
}

func (s *gousbSession) Close() error {
	if s.iface != nil {
		s.iface.Close()
	}
	var closeErrors []error
	if s.config != nil {
		closeErrors = append(closeErrors, s.config.Close())
	}
	if s.device != nil {
		closeErrors = append(closeErrors, s.device.Close())
	}
	if s.usbContext != nil {
		closeErrors = append(closeErrors, s.usbContext.Close())
	}
	return errors.Join(closeErrors...)
}

// Open enumerates only the configured CH341 VID/PID and claims one interface
// containing both bulk IN and bulk OUT endpoints.
func Open(ctx context.Context, cfg config.Config) (mfi.I2CTransport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	usbContext := gousb.NewContext()
	device, err := usbContext.OpenDeviceWithVIDPID(gousb.ID(cfg.USBVendor), gousb.ID(cfg.USBProduct))
	if err != nil {
		_ = usbContext.Close()
		return nil, safeUSBError("open configured CH341", classifyGousbError(err))
	}
	if device == nil {
		_ = usbContext.Close()
		return nil, fmt.Errorf("open configured CH341 %04x:%04x: %w", cfg.USBVendor, cfg.USBProduct, ErrUSBMissing)
	}
	if err := ctx.Err(); err != nil {
		_ = device.Close()
		_ = usbContext.Close()
		return nil, err
	}

	configNumber, interfaceNumber, alternate, inputNumber, outputNumber, ok := findBulkEndpoints(device.Desc)
	if !ok {
		_ = device.Close()
		_ = usbContext.Close()
		return nil, fmt.Errorf("open configured CH341: no interface has both bulk IN and OUT endpoints")
	}
	usbConfig, err := device.Config(configNumber)
	if err != nil {
		_ = device.Close()
		_ = usbContext.Close()
		return nil, safeUSBError("claim CH341 configuration", classifyGousbError(err))
	}
	iface, err := usbConfig.Interface(interfaceNumber, alternate)
	if err != nil {
		_ = usbConfig.Close()
		_ = device.Close()
		_ = usbContext.Close()
		return nil, safeUSBError("claim CH341 interface", classifyGousbError(err))
	}
	input, err := iface.InEndpoint(inputNumber)
	if err != nil {
		iface.Close()
		_ = usbConfig.Close()
		_ = device.Close()
		_ = usbContext.Close()
		return nil, safeUSBError("open CH341 bulk input", classifyGousbError(err))
	}
	output, err := iface.OutEndpoint(outputNumber)
	if err != nil {
		iface.Close()
		_ = usbConfig.Close()
		_ = device.Close()
		_ = usbContext.Close()
		return nil, safeUSBError("open CH341 bulk output", classifyGousbError(err))
	}
	session := &gousbSession{
		usbContext: usbContext,
		device:     device,
		config:     usbConfig,
		iface:      iface,
		input:      input,
		output:     output,
	}
	transport, err := newTransport(session, cfg.I2CSpeedKHz)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	return transport, nil
}

func findBulkEndpoints(desc *gousb.DeviceDesc) (
	configNumber int,
	interfaceNumber int,
	alternate int,
	inputNumber int,
	outputNumber int,
	ok bool,
) {
	configNumbers := make([]int, 0, len(desc.Configs))
	for number := range desc.Configs {
		configNumbers = append(configNumbers, number)
	}
	sort.Ints(configNumbers)
	for _, number := range configNumbers {
		usbConfig := desc.Configs[number]
		for _, interfaceDesc := range usbConfig.Interfaces {
			for _, setting := range interfaceDesc.AltSettings {
				input, output := -1, -1
				for _, endpoint := range setting.Endpoints {
					if endpoint.TransferType != gousb.TransferTypeBulk {
						continue
					}
					switch endpoint.Direction {
					case gousb.EndpointDirectionIn:
						if input < 0 {
							input = endpoint.Number
						}
					case gousb.EndpointDirectionOut:
						if output < 0 {
							output = endpoint.Number
						}
					}
				}
				if input >= 0 && output >= 0 {
					return number, setting.Number, setting.Alternate, input, output, true
				}
			}
		}
	}
	return 0, 0, 0, 0, 0, false
}

func transferError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if contextError := ctx.Err(); contextError != nil {
		return contextError
	}
	var status gousb.TransferStatus
	if errors.As(err, &status) && (status == gousb.TransferTimedOut || status == gousb.TransferCancelled) {
		return context.DeadlineExceeded
	}
	if errors.As(err, &status) && status == gousb.TransferNoDevice {
		return ErrUSBMissing
	}
	return safeUSBError("CH341 USB transfer", classifyGousbError(err))
}

func classifyGousbError(err error) error {
	var usbError gousb.Error
	if errors.As(err, &usbError) {
		switch usbError {
		case gousb.ErrorAccess:
			return ErrUSBPermission
		case gousb.ErrorBusy:
			return ErrUSBBusy
		case gousb.ErrorNoDevice, gousb.ErrorNotFound:
			return ErrUSBMissing
		}
	}
	return err
}
