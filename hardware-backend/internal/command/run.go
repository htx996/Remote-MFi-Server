// SPDX-License-Identifier: GPL-3.0-only

// Package command implements the bounded JSON stdin/stdout contract used by
// the Python Remote-MFi-Server process.
package command

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/htx996/remote-mfi-server/hardware-backend/internal/config"
	"github.com/htx996/remote-mfi-server/hardware-backend/internal/mfi"
)

const (
	maximumInputBytes = 4 * 1024
	resetDetail       = "USB session reopened; physical MFi RST was not pulsed to preserve I2C address 0x11"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// OpenFunc opens the configured CH341 transport.
type OpenFunc func(context.Context, config.Config) (mfi.I2CTransport, error)

type emptyRequest struct{}

type signRequest struct {
	Challenge string `json:"challenge"`
	RequestID string `json:"requestId"`
}

type probeResponse struct {
	USBID             string `json:"usbId"`
	I2CAddress        uint8  `json:"i2cAddress"`
	DeviceVersion     uint8  `json:"deviceVersion"`
	ProtocolMajor     uint8  `json:"protocolMajor"`
	CertificateLength uint16 `json:"certificateLength"`
}

type certificateResponse struct {
	Type          string `json:"type"`
	ProtocolMajor uint8  `json:"protocolMajor"`
	Certificate   string `json:"certificate"`
}

type signResponse struct {
	Signature string `json:"signature"`
}

type resetResponse struct {
	Detail string `json:"detail"`
}

// Run executes one operation and returns a process exit code. stdout remains
// empty on every failure and contains exactly one JSON object on success.
func Run(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	open OpenFunc,
) int {
	if len(args) == 1 && args[0] == "--version" {
		_, _ = fmt.Fprintf(stdout, "mfi-backend version=%s commit=%s buildDate=%s\n", Version, Commit, BuildDate)
		return 0
	}
	if len(args) != 1 {
		return fail(stderr, "command", "exactly one operation is required")
	}
	operation := args[0]
	if operation != "probe" && operation != "certificate" && operation != "sign" && operation != "reset" {
		return fail(stderr, "command", "unsupported operation")
	}

	var request any
	switch operation {
	case "sign":
		request = &signRequest{}
	default:
		request = &emptyRequest{}
	}
	if err := decodeOneObject(stdin, request); err != nil {
		return fail(stderr, operation, "invalid JSON input")
	}

	var challenge []byte
	if sign, ok := request.(*signRequest); ok {
		if strings.TrimSpace(sign.RequestID) == "" || sign.Challenge == "" {
			return fail(stderr, operation, "challenge and requestId are required")
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(sign.Challenge)
		if err != nil || len(decoded) == 0 {
			return fail(stderr, operation, "challenge must be valid non-empty Base64")
		}
		challenge = decoded
	}

	cfg, err := config.FromEnvironment(os.LookupEnv)
	if err != nil {
		return fail(stderr, operation, "hardware configuration is invalid")
	}
	if open == nil {
		return fail(stderr, operation, "hardware transport is unavailable")
	}
	transport, err := open(ctx, cfg)
	if err != nil {
		return fail(stderr, operation, "hardware transport could not be opened")
	}
	driver, err := mfi.NewDriver(transport, cfg.I2CAddress)
	if err != nil {
		_ = transport.Close()
		return fail(stderr, operation, "hardware driver could not be initialized")
	}

	response, operationErr := executeOperation(ctx, operation, cfg, driver, challenge)
	closeErr := driver.Close()
	if operationErr != nil {
		return fail(stderr, operation, "hardware operation failed")
	}
	if closeErr != nil {
		return fail(stderr, operation, "hardware transport could not be closed")
	}
	if err := json.NewEncoder(stdout).Encode(response); err != nil {
		return fail(stderr, operation, "JSON output failed")
	}
	return 0
}

func executeOperation(
	ctx context.Context,
	operation string,
	cfg config.Config,
	driver *mfi.Driver,
	challenge []byte,
) (any, error) {
	switch operation {
	case "probe":
		probe, err := driver.Probe(ctx)
		if err != nil {
			return nil, err
		}
		return probeResponse{
			USBID:             fmt.Sprintf("%04x:%04x", cfg.USBVendor, cfg.USBProduct),
			I2CAddress:        probe.Address,
			DeviceVersion:     probe.DeviceVersion,
			ProtocolMajor:     probe.ProtocolMajor,
			CertificateLength: probe.CertificateLength,
		}, nil
	case "certificate":
		probe, err := driver.Probe(ctx)
		if err != nil {
			return nil, err
		}
		certificate, err := driver.ReadCertificate(ctx)
		if err != nil {
			return nil, err
		}
		return certificateResponse{
			Type:          "mfi",
			ProtocolMajor: probe.ProtocolMajor,
			Certificate:   base64.StdEncoding.EncodeToString(certificate),
		}, nil
	case "sign":
		signature, err := driver.SignChallenge(ctx, challenge)
		if err != nil {
			return nil, err
		}
		return signResponse{Signature: base64.StdEncoding.EncodeToString(signature)}, nil
	case "reset":
		if _, err := driver.Probe(ctx); err != nil {
			return nil, err
		}
		return resetResponse{Detail: resetDetail}, nil
	default:
		return nil, fmt.Errorf("unsupported operation")
	}
}

func decodeOneObject(reader io.Reader, destination any) error {
	limited := io.LimitReader(reader, maximumInputBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) > maximumInputBytes {
		return fmt.Errorf("input exceeds %d bytes", maximumInputBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("input contains multiple JSON values")
		}
		return err
	}
	return nil
}

func fail(stderr io.Writer, operation, reason string) int {
	_, _ = fmt.Fprintf(stderr, "mfi-backend: %s failed: %s\n", operation, reason)
	return 1
}
