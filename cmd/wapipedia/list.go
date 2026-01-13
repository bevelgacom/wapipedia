package main

import (
	"fmt"

	"github.com/bevelgacom/wapipedia/pkg/wikipedia"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List available Wikipedia dumps",
	Long:  `List all available Wikipedia dumps that can be downloaded.`,
	Run: func(cmd *cobra.Command, args []string) {
		runList()
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}

func runList() {
	fmt.Println("Available Wikipedia dumps:")
	fmt.Println()
	for name, url := range wikipedia.ListAvailableDumps() {
		fmt.Printf("  %-10s %s\n", name, url)
	}
	fmt.Println()
	fmt.Println("Use 'wapipedia download -lang <name>' to download a dump.")
	fmt.Println()
	fmt.Println("Note: The 'simple' and 'top100' dumps are small and good for testing.")
	fmt.Println("Full language dumps (en, nl, fr, etc.) can be very large (10-90 GB).")
}
