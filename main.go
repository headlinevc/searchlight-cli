package main

import (
	"fmt"
	"os"

	"github.com/headlinevc/searchlight-cli/cmd"
	"github.com/headlinevc/searchlight-cli/internal/errors"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	cmd.SetBuildInfo(version, commit, buildDate)

	if err := cmd.Execute(); err != nil {
		exit := errors.ExitCodeFor(err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exit)
	}
}
