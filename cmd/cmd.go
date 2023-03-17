package cmd

import (
	"github.com/bob2325168/spider/cmd/master"
	"github.com/bob2325168/spider/cmd/worker"
	"github.com/bob2325168/spider/version"
	"github.com/spf13/cobra"
	"go-micro.dev/v4/util/log"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "print version",
	Long:  "print version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		version.Printer()
	},
}

func Execute() {
	var rootCmd = &cobra.Command{Use: "crawler"}
	rootCmd.AddCommand(master.Cmd, worker.Cmd, versionCmd)
	if err := rootCmd.Execute(); err != nil {
		log.Infof("crawler command execute failed: ", err)
	}
}
