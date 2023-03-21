package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
)

// Version information.
var (
	BuildTS   = "None"
	GitHash   = "None"
	GitBranch = "None"
	Version   = "None"
)

var VersionCmd = &cobra.Command{
	Use:   "version",
	Short: "print version",
	Long:  "print version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		Printer()
	},
}

func GetVersion() string {
	if GitHash != "" {
		h := GitHash
		if len(h) > 7 {
			h = h[:7]
		}
		return fmt.Sprintf("%s-%s", Version, h)
	}
	return Version
}

// Printer print build version
func Printer() {
	fmt.Println("Version:          ", GetVersion())
	fmt.Println("Git Branch:       ", GitBranch)
	fmt.Println("Git Commit:       ", GitHash)
	fmt.Println("Build Time (UTC): ", BuildTS)
}
