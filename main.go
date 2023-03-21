package main

import (
	"github.com/bob2325168/spider/cmd"
	"github.com/spf13/cobra"
	"go-micro.dev/v4/util/log"
)

func execute() {
	var rootCmd = &cobra.Command{Use: "crawler"}
	rootCmd.AddCommand(cmd.MasterCmd, cmd.WorkerCmd, cmd.VersionCmd)
	if err := rootCmd.Execute(); err != nil {
		log.Infof("crawler command execute failed: ", err)
	}
}

func main() {
	execute()
}
