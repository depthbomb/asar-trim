package main

import (
	"context"
	"fmt"
	"os"

	"github.com/depthbomb/asar-trim/internal/app"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	cmd := app.New(displayVersion())
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func displayVersion() string {
	if commit == "unknown" && buildDate == "unknown" {
		return version
	}
	return fmt.Sprintf("%s (commit %s, built %s)", version, commit, buildDate)
}
