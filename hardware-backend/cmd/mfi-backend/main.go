// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/htx996/remote-mfi-server/hardware-backend/internal/ch341"
	"github.com/htx996/remote-mfi-server/hardware-backend/internal/command"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(command.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, ch341.Open))
}
