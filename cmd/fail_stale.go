package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/google/subcommands"
	"github.com/synadia-labs/go-bench-away/v1/client"
)

type failStaleCmd struct {
	baseCommand
}

func failStaleCommand() subcommands.Command {
	return &failStaleCmd{
		baseCommand: baseCommand{
			name:     "fail-stale",
			synopsis: "Mark all stale Submitted/Running jobs as Failed",
			usage:    "fail-stale [options]\n",
		},
	}
}

func (cmd *failStaleCmd) SetFlags(f *flag.FlagSet) {
}

func (cmd *failStaleCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if rootOptions.verbose {
		fmt.Printf("%s args: %v\n", cmd.name, f.Args())
	}

	c, err := client.NewClient(
		rootOptions.natsServerUrl,
		rootOptions.credentials,
		rootOptions.namespace,
		client.InitJobsQueue(),
		client.InitJobsRepository(),
		client.Verbose(rootOptions.verbose),
	)

	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return subcommands.ExitFailure
	}
	defer c.Close()

	fmt.Println("Scanning for stale Submitted/Running jobs...")

	updated, err := c.FailStaleJobs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return subcommands.ExitFailure
	}

	fmt.Printf("Marked %d stale jobs as Failed\n", updated)
	return subcommands.ExitSuccess
}
