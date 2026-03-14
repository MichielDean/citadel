package main

import "github.com/spf13/cobra"

var cfgPath string

var rootCmd = &cobra.Command{
	Use:   "farm",
	Short: "Bullet Farm orchestrator",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgPath, "config", "config.yaml", "path to farm config file")
}
