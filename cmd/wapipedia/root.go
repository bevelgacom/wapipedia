package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "wapipedia",
	Short: "WAPipedia - Wikipedia for WAP devices",
	Long: `WAPipedia is a lightweight Wikipedia server designed for WAP devices.
It serves Wikipedia content from ZIM files in WML format`,
	Run: func(cmd *cobra.Command, args []string) {
		// Default to serve command when no subcommand is provided
		runServe()
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
