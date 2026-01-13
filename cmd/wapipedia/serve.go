package main

import (
	"log"
	"net/http"
	"os"

	"github.com/bevelgacom/wapipedia/internal/server"
	"github.com/labstack/echo/v4"
	"github.com/spf13/cobra"
)

var (
	zimPath string
	port    string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the WAP server",
	Long: `Start the Wapipedia WAP server to serve Wikipedia content
to legacy mobile devices.`,
	Example: `  wapipedia serve
  wapipedia serve -zim ./data/wikipedia.zim -port 8080`,
	Run: func(cmd *cobra.Command, args []string) {
		runServe()
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)

	defaultZim := os.Getenv("WAPIPEDIA_ZIM")
	if defaultZim == "" {
		defaultZim = "./data/wikipedia.zim"
	}

	serveCmd.Flags().StringVarP(&zimPath, "zim", "z", defaultZim, "Path to Wikipedia ZIM file")
	serveCmd.Flags().StringVarP(&port, "port", "p", "8080", "Server port")

	// Also add flags to root command for default behavior
	rootCmd.Flags().StringVarP(&zimPath, "zim", "z", defaultZim, "Path to Wikipedia ZIM file")
	rootCmd.Flags().StringVarP(&port, "port", "p", "8080", "Server port")
}

func runServe() {
	// Initialize Wikipedia if ZIM file exists
	if _, err := os.Stat(zimPath); err == nil {
		log.Printf("Loading Wikipedia from %s...", zimPath)
		if err := server.InitWikipedia(zimPath); err != nil {
			log.Printf("Warning: Failed to load Wikipedia: %v", err)
			log.Println("Wikipedia features will be disabled. Use 'wapipedia download' to get dumps.")
		} else {
			log.Println("Wikipedia loaded successfully")
		}
	} else {
		log.Println("No Wikipedia ZIM file found. Wikipedia features disabled.")
		log.Println("Use 'wapipedia download -lang simple' to download a Wikipedia dump.")
	}

	e := echo.New()

	// Wikipedia routes
	server.RegisterWikiRoutes(e)

	log.Printf("Starting Wapipedia server on port %s...", port)
	if err := e.Start(":" + port); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
