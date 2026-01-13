package main

import (
	"fmt"
	"os"

	"github.com/bevelgacom/wapipedia/pkg/wikipedia"
	"github.com/spf13/cobra"
)

var (
	downloadLang string
	downloadDest string
)

var downloadCmd = &cobra.Command{
	Use:   "download",
	Short: "Download Wikipedia dump files",
	Long: `Download Wikipedia dump files from the Kiwix repository.
Various language editions and sizes are available.`,
	Example: `  wapipedia download -lang simple -dest ./data
  wapipedia download -lang en -dest ./data
  wapipedia download -lang top100 -dest ./data`,
	Run: func(cmd *cobra.Command, args []string) {
		runDownload()
	},
}

func init() {
	rootCmd.AddCommand(downloadCmd)

	downloadCmd.Flags().StringVarP(&downloadLang, "lang", "l", "simple", "Language/dump to download (simple, en, nl, fr, de, es, top100)")
	downloadCmd.Flags().StringVarP(&downloadDest, "dest", "d", "./data", "Destination directory for download")
}

func runDownload() {
	fmt.Printf("Downloading Wikipedia dump '%s' to %s...\n", downloadLang, downloadDest)

	path, err := wikipedia.DownloadDump(downloadLang, downloadDest, func(progress wikipedia.DownloadProgress) {
		if progress.TotalBytes > 0 {
			fmt.Printf("\rDownloading: %.1f%% (%d MB / %d MB)",
				progress.Percentage,
				progress.DownloadedBytes/(1024*1024),
				progress.TotalBytes/(1024*1024))
		} else {
			fmt.Printf("\rDownloaded: %d MB", progress.DownloadedBytes/(1024*1024))
		}
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nDownload complete: %s\n", path)
	fmt.Printf("\nTo use this dump, run:\n  WAPIPEDIA_ZIM=%s wapipedia serve\n", path)
	fmt.Printf("Or:\n  wapipedia serve -zim %s\n", path)
}
