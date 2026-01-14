package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/bevelgacom/wapipedia/pkg/wikipedia"
	"github.com/spf13/cobra"
)

var (
	indexZimPath    string
	indexOutputPath string
)

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Build search index from a ZIM file",
	Long: `Build a persistent Bluge search index from a Wikipedia ZIM file.
The index enables fast search queries without loading the entire ZIM into memory.

The index is stored next to the ZIM file with a .bluge extension by default.`,
	Example: `  wapipedia index -z ./data/wikipedia.zim
  wapipedia index -z ./data/wikipedia.zim -o ./data/wikipedia.bluge`,
	Run: func(cmd *cobra.Command, args []string) {
		runIndex()
	},
}

func init() {
	rootCmd.AddCommand(indexCmd)

	defaultZim := os.Getenv("WAPIPEDIA_ZIM")
	if defaultZim == "" {
		defaultZim = "./data/wikipedia.zim"
	}

	indexCmd.Flags().StringVarP(&indexZimPath, "zim", "z", defaultZim, "Path to Wikipedia ZIM file")
	indexCmd.Flags().StringVarP(&indexOutputPath, "output", "o", "", "Output path for index (default: ZIM path with .bluge extension)")
}

func runIndex() {
	// Check if ZIM file exists
	if _, err := os.Stat(indexZimPath); os.IsNotExist(err) {
		log.Fatalf("ZIM file not found: %s", indexZimPath)
	}

	// Determine output path
	outputPath := indexOutputPath
	if outputPath == "" {
		outputPath = wikipedia.DefaultIndexPath(indexZimPath)
	}

	fmt.Printf("Building search index...\n")
	fmt.Printf("  ZIM file: %s\n", indexZimPath)
	fmt.Printf("  Output:   %s\n", outputPath)
	fmt.Println()

	startTime := time.Now()

	if err := wikipedia.BuildBlugeIndex(indexZimPath, outputPath); err != nil {
		log.Fatalf("Failed to build index: %v", err)
	}

	elapsed := time.Since(startTime)
	fmt.Printf("\nIndex built successfully in %s\n", elapsed.Round(time.Second))
}
