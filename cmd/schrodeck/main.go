// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command schrodeck keeps Stream Deck setups identical across computers.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/csmarshall/schrodeck/internal/cli"
	"github.com/csmarshall/schrodeck/internal/logging"
	"github.com/csmarshall/schrodeck/internal/version"
)

func main() {
	logger, err := logging.New(os.Stderr, os.Getenv(logging.EnvLevel))
	if err != nil {
		fmt.Fprintln(os.Stderr, "schrodeck:", err)
		os.Exit(cli.ExitUsage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], cli.Env{
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Version: version.Version,
		Logger:  logger,
	})
	stop()
	os.Exit(code)
}
