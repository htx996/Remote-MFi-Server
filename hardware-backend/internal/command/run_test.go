package command

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/htx996/remote-mfi-server/hardware-backend/internal/ch341"
	"github.com/htx996/remote-mfi-server/hardware-backend/internal/config"
	"github.com/htx996/remote-mfi-server/hardware-backend/internal/mfi"
)

type commandStep struct {
	write    []byte
	readLen  int
	response []byte
}

type commandTransport struct {
	t      testing.TB
	steps  []commandStep
	closed bool
}

func (t *commandTransport) Transaction(
	_ context.Context,
	address uint8,
	writeData []byte,
	readLength int,
) ([]byte, error) {
	t.t.Helper()
	if address != 0x11 {
		t.t.Fatalf("I2C address = 0x%02x, want configured 0x11", address)
	}
	if len(t.steps) == 0 {
		t.t.Fatalf("unexpected transaction write=%x read=%d", writeData, readLength)
	}
	step := t.steps[0]
	t.steps = t.steps[1:]
	if !bytes.Equal(writeData, step.write) || readLength != step.readLen {
		t.t.Fatalf("transaction write=%x read=%d, want write=%x read=%d", writeData, readLength, step.write, step.readLen)
	}
	return append([]byte(nil), step.response...), nil
}

func (t *commandTransport) Close() error {
	t.closed = true
	return nil
}

func commandSelect(register byte) commandStep { return commandStep{write: []byte{register}} }

func commandRead(response ...byte) commandStep {
	return commandStep{readLen: len(response), response: append([]byte(nil), response...)}
}

func commandWrite(register byte, data []byte) commandStep {
	return commandStep{write: append([]byte{register}, data...)}
}

func probeSteps() []commandStep {
	return []commandStep{
		commandSelect(0x00), commandRead(0x03),
		commandSelect(0x02), commandRead(0x03),
		commandSelect(0x30), commandRead(0x00, 0x03),
	}
}

func openWithSteps(t testing.TB, steps ...commandStep) (OpenFunc, *commandTransport) {
	t.Helper()
	transport := &commandTransport{t: t, steps: append([]commandStep(nil), steps...)}
	return func(_ context.Context, cfg config.Config) (mfi.I2CTransport, error) {
		if cfg.USBVendor != 0x1a86 || cfg.USBProduct != 0x5512 || cfg.I2CAddress != 0x11 || cfg.I2CSpeedKHz != 100 {
			t.Fatalf("Open() config = %#v, want board defaults", cfg)
		}
		return transport, nil
	}, transport
}

func runCommand(t testing.TB, args []string, input string, open OpenFunc) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), args, strings.NewReader(input), &stdout, &stderr, open)
	return exitCode, stdout.String(), stderr.String()
}

func TestRunVersionDoesNotOpenUSB(t *testing.T) {
	opened := false
	open := func(context.Context, config.Config) (mfi.I2CTransport, error) {
		opened = true
		return nil, errors.New("must not open")
	}
	exitCode, stdout, stderr := runCommand(t, []string{"--version"}, "", open)
	if exitCode != 0 || opened || stderr != "" {
		t.Fatalf("Run(--version) = exit %d opened=%v stderr=%q", exitCode, opened, stderr)
	}
	if !strings.Contains(stdout, "mfi-backend") || !strings.Contains(stdout, "version") {
		t.Fatalf("version output = %q", stdout)
	}
}

func TestRunRejectsMissingOrUnknownOperation(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}} {
		exitCode, stdout, _ := runCommand(t, args, "{}", nil)
		if exitCode == 0 || stdout != "" {
			t.Fatalf("Run(%v) = exit %d stdout %q, want failure without JSON", args, exitCode, stdout)
		}
	}
}

func TestRunRejectsMalformedTrailingAndOversizedJSON(t *testing.T) {
	inputs := []string{
		"{",
		"{} {}",
		strings.Repeat("x", maximumInputBytes+1),
	}
	for _, input := range inputs {
		exitCode, stdout, _ := runCommand(t, []string{"probe"}, input, nil)
		if exitCode == 0 || stdout != "" {
			t.Fatalf("Run() accepted invalid JSON of length %d", len(input))
		}
	}
}

func TestRunRejectsInvalidInputBeforeOpeningUSB(t *testing.T) {
	inputs := []struct{ operation, input string }{
		{"probe", "null"}, {"probe", "[]"}, {"probe", `"text"`},
		{"sign", fmt.Sprintf(`{"challenge":%q,"requestId":"id"}`, base64.StdEncoding.EncodeToString(make([]byte, 129)))},
		{"sign", fmt.Sprintf(`{"challenge":"AQ==","requestId":%q}`, strings.Repeat("x", 129))},
	}
	for _, input := range inputs {
		t.Run(input.operation+input.input[:min(len(input.input), 12)], func(t *testing.T) {
			opened := false
			open := func(context.Context, config.Config) (mfi.I2CTransport, error) {
				opened = true
				return nil, errors.New("should not open")
			}
			exit, stdout, _ := runCommand(t, []string{input.operation}, input.input, open)
			if exit == 0 || opened || stdout != "" {
				t.Fatalf("invalid input reached USB: exit=%d opened=%v stdout=%q", exit, opened, stdout)
			}
		})
	}
}

func TestRunProbeEmitsOnlyNonSensitiveHardwareFields(t *testing.T) {
	open, transport := openWithSteps(t, probeSteps()...)
	exitCode, stdout, stderr := runCommand(t, []string{"probe"}, "{}", open)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("Run(probe) = exit %d stderr=%q", exitCode, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("probe stdout is not JSON: %v", err)
	}
	if len(got) != 5 || got["usbId"] != "1a86:5512" || got["i2cAddress"] != float64(0x11) ||
		got["deviceVersion"] != float64(3) || got["protocolMajor"] != float64(3) ||
		got["certificateLength"] != float64(3) {
		t.Fatalf("probe JSON = %#v", got)
	}
	if !transport.closed || len(transport.steps) != 0 {
		t.Fatalf("probe transport closed=%v remaining=%d", transport.closed, len(transport.steps))
	}
}

func TestRunCertificateEmitsRealBase64Certificate(t *testing.T) {
	certificate := []byte{0xde, 0xad, 0xbe}
	steps := append(probeSteps(),
		commandSelect(0x30), commandRead(0x00, 0x03),
		commandSelect(0x31), commandRead(certificate...),
	)
	open, transport := openWithSteps(t, steps...)
	exitCode, stdout, stderr := runCommand(t, []string{"certificate"}, "{}", open)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("Run(certificate) = exit %d stderr=%q", exitCode, stderr)
	}
	var got struct {
		Type          string `json:"type"`
		ProtocolMajor int    `json:"protocolMajor"`
		Certificate   string `json:"certificate"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("certificate stdout is not JSON: %v", err)
	}
	if got.Type != "mfi" || got.ProtocolMajor != 3 || got.Certificate != base64.StdEncoding.EncodeToString(certificate) {
		t.Fatalf("certificate JSON = %#v", got)
	}
	if !transport.closed || len(transport.steps) != 0 {
		t.Fatalf("certificate transport closed=%v remaining=%d", transport.closed, len(transport.steps))
	}
}

func TestRunSignRequiresChallengeAndRequestIDAndEmitsSignature(t *testing.T) {
	for _, input := range []string{
		`{"requestId":"request-1"}`,
		`{"challenge":"AQID"}`,
		`{"challenge":"not-base64","requestId":"request-1"}`,
	} {
		exitCode, stdout, _ := runCommand(t, []string{"sign"}, input, nil)
		if exitCode == 0 || stdout != "" {
			t.Fatalf("Run(sign) accepted invalid request %s", input)
		}
	}

	challenge := []byte{0x01, 0x02, 0x03}
	signature := []byte{0xa1, 0xb2, 0xc3, 0xd4}
	steps := []commandStep{
		commandWrite(0x20, []byte{0x00, 0x03}),
		commandWrite(0x21, challenge),
		commandWrite(0x10, []byte{0x01}),
		commandSelect(0x10), commandRead(0x10),
		commandSelect(0x11), commandRead(0x00, 0x04),
		commandSelect(0x12), commandRead(signature...),
	}
	open, transport := openWithSteps(t, steps...)
	input := fmt.Sprintf(`{"challenge":%q,"requestId":"request-1"}`, base64.StdEncoding.EncodeToString(challenge))
	exitCode, stdout, stderr := runCommand(t, []string{"sign"}, input, open)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("Run(sign) = exit %d stderr=%q", exitCode, stderr)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("sign stdout is not JSON: %v", err)
	}
	if len(got) != 1 || got["signature"] != base64.StdEncoding.EncodeToString(signature) {
		t.Fatalf("sign JSON = %#v", got)
	}
	if !transport.closed || len(transport.steps) != 0 {
		t.Fatalf("sign transport closed=%v remaining=%d", transport.closed, len(transport.steps))
	}
}

func TestRunResetProbesWithoutPulsingPhysicalRST(t *testing.T) {
	open, _ := openWithSteps(t, probeSteps()...)
	exitCode, stdout, stderr := runCommand(t, []string{"reset"}, "{}", open)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("Run(reset) = exit %d stderr=%q", exitCode, stderr)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("reset stdout is not JSON: %v", err)
	}
	want := "USB session reopened; physical MFi RST was not pulsed; probed I2C address 0x11"
	if len(got) != 1 || got["detail"] != want {
		t.Fatalf("reset JSON = %#v, want detail %q", got, want)
	}
}

func TestRunHardwareFailureHasSafeStderrAndNoSuccessJSON(t *testing.T) {
	secret := "certificate-challenge-signature-deadbeef"
	open := func(context.Context, config.Config) (mfi.I2CTransport, error) {
		return nil, fmt.Errorf("%s", secret)
	}
	exitCode, stdout, stderr := runCommand(t, []string{"probe"}, "{}", open)
	if exitCode == 0 || stdout != "" {
		t.Fatalf("Run(probe) = exit %d stdout=%q, want failure without success JSON", exitCode, stdout)
	}
	if strings.Contains(stderr, secret) || !strings.Contains(stderr, "probe") {
		t.Fatalf("hardware failure stderr is unsafe or unhelpful: %q", stderr)
	}
}

func TestRunReportsSafeUSBErrorCategory(t *testing.T) {
	for _, tc := range []struct {
		err      error
		category string
	}{
		{ch341.ErrUSBPermission, "usb_permission"}, {ch341.ErrUSBBusy, "usb_busy"},
		{ch341.ErrUSBMissing, "usb_missing"}, {context.DeadlineExceeded, "usb_timeout"},
	} {
		open := func(context.Context, config.Config) (mfi.I2CTransport, error) {
			return nil, fmt.Errorf("secret-payload: %w", tc.err)
		}
		code, stdout, stderr := runCommand(t, []string{"probe"}, "{}", open)
		if code == 0 || stdout != "" || !strings.Contains(stderr, tc.category) || strings.Contains(stderr, "secret-payload") {
			t.Fatalf("unsafe or missing category: %d %q %q", code, stdout, stderr)
		}
	}
}

func TestRunNeverReadsOrPrintsBearerToken(t *testing.T) {
	secret := "BEARER-TOKEN-MUST-NOT-LEAK"
	t.Setenv("MFI_BEARER_TOKEN", secret)
	open, _ := openWithSteps(t, probeSteps()...)
	exitCode, stdout, stderr := runCommand(t, []string{"probe"}, "{}", open)
	if exitCode != 0 {
		t.Fatalf("Run(probe) exit = %d stderr=%q", exitCode, stderr)
	}
	if strings.Contains(stdout, secret) || strings.Contains(stderr, secret) {
		t.Fatalf("Bearer Token leaked: stdout=%q stderr=%q", stdout, stderr)
	}
}
