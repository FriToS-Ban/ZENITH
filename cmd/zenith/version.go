package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print ZENITH version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("zenith %s\n", version)
	},
}
