package main

import (
	"os"

	gcphcpcli "github.com/openshift-online/gcp-hcp-ctl/pkg/cli"
)

func main() {
	if err := gcphcpcli.Execute(); err != nil {
		os.Exit(1)
	}
}
