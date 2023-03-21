package main

import (
	"github.com/bob2325168/spider/cmd"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func execute() {
	var rootCmd = &cobra.Command{Use: "crawler"}
	rootCmd.AddCommand(cmd.MasterCmd, cmd.WorkerCmd, cmd.VersionCmd)
	if err := rootCmd.Execute(); err != nil {
		zap.L().Debug("crawler command excute failed", zap.Error(err))
	}
}

func main() {
	execute()
}
