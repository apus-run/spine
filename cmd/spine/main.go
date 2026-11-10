package main

import (
	"log"

	"github.com/spf13/cobra"

	"github.com/apus-run/spine/cmd/spine/v3/internal/change"
	"github.com/apus-run/spine/cmd/spine/v3/internal/project"
	"github.com/apus-run/spine/cmd/spine/v3/internal/proto"
	"github.com/apus-run/spine/cmd/spine/v3/internal/run"
	"github.com/apus-run/spine/cmd/spine/v3/internal/upgrade"
)

var rootCmd = &cobra.Command{
	Use:     "spine",
	Short:   "Spine: A toolkit for Go microservices.",
	Long:    `Spine: A toolkit for Go microservices.`,
	Version: release,
}

func init() {
	rootCmd.AddCommand(project.CmdNew)
	rootCmd.AddCommand(proto.CmdProto)
	rootCmd.AddCommand(upgrade.CmdUpgrade)
	rootCmd.AddCommand(change.CmdChange)
	rootCmd.AddCommand(run.CmdRun)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("Failed to execute command: %v", err)
	}
}
