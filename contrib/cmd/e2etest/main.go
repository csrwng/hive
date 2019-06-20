package main

import (
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/hive/contrib/pkg/e2e"
)

func main() {
	log.SetOutput(os.Stdout)
	log.SetLevel(log.DebugLevel)

	cmd := newE2ECommand()

	err := cmd.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error occurred: %v\n", err)
		os.Exit(1)
	}
}

func newE2ECommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "e2etest SUB-COMMAND",
		Short: "Commands used for Hive e2e testing",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Usage()
		},
	}
	cmd.AddCommand(e2e.CreateCluster())
	// cmd.AddCommand(e2e.DestroyCluster())
	return cmd
}
