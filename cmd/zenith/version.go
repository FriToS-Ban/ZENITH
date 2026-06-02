package main

import (
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print ZENITH version",
	Run: func(cmd *cobra.Command, args []string) {
		printBanner(version)
	},
}
