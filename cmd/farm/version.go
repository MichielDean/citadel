package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Set via -ldflags at build time.
var version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the farm version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("farm", version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
