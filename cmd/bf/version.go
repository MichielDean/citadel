package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Set via -ldflags at build time.
var version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the bf version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("bf", version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
