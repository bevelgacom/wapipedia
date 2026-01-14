package main

import (
	"log"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/bevelgacom/wapipedia/internal/server"
	"github.com/labstack/echo/v4"
	"github.com/spf13/cobra"
)

var (
	zimPath    string
	port       string
	lowMemory  bool
	gcInterval int
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the WAP server",
	Long: `Start the WAPipedia WAP server to serve Wikipedia content
to legacy mobile devices.`,
	Example: `  wapipedia serve
  wapipedia serve -zim ./data/wikipedia.zim -port 8080
  wapipedia serve --low-memory  # For systems with 512MB RAM or less`,
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
	serveCmd.Flags().BoolVar(&lowMemory, "low-memory", false, "Enable low-memory optimizations for systems with 512MB RAM or less")
	serveCmd.Flags().IntVar(&gcInterval, "gc-interval", 60, "Garbage collection interval in seconds (0 to disable)")

	// Also add flags to root command for default behavior
	rootCmd.Flags().StringVarP(&zimPath, "zim", "z", defaultZim, "Path to Wikipedia ZIM file")
	rootCmd.Flags().StringVarP(&port, "port", "p", "8080", "Server port")
}

func runServe() {
	// Memory optimization settings for low-memory systems
	if lowMemory {
		log.Println("Low-memory mode enabled")
		// Limit to 2 threads to reduce memory overhead
		runtime.GOMAXPROCS(2)
		log.Printf("GOMAXPROCS set to %d", runtime.GOMAXPROCS(0))

		// Set aggressive GC target
		debug.SetGCPercent(20)
		log.Println("GC percent set to 20%")

		// Set memory limit hint (350MB for 512MB system, leave room for OS)
		debug.SetMemoryLimit(350 * 1024 * 1024)
		log.Println("Memory limit set to 350MB")
	}

	// Start periodic GC if enabled
	if gcInterval > 0 {
		log.Printf("Starting periodic GC every %d seconds", gcInterval)
		go periodicGC(time.Duration(gcInterval) * time.Second)
	}

	// Initialize Wikipedia if ZIM file exists
	if _, err := os.Stat(zimPath); err == nil {
		log.Printf("Loading Wikipedia from %s...", zimPath)
		if err := server.InitWikipedia(zimPath); err != nil {
			log.Printf("Warning: Failed to load Wikipedia: %v", err)
			log.Println("Wikipedia features will be disabled. Use 'wapipedia download' to get dumps.")
		} else {
			log.Println("Wikipedia loaded successfully")
			logMemStats()
		}
	} else {
		log.Println("No Wikipedia ZIM file found. Wikipedia features disabled.")
		log.Println("Use 'wapipedia download -lang simple' to download a Wikipedia dump.")
	}

	e := echo.New()

	// Wikipedia routes
	server.RegisterWikiRoutes(e)

	log.Printf("Starting WAPipedia server on port %s...", port)
	if err := e.Start(":" + port); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// periodicGC runs garbage collection periodically to keep memory usage low
func periodicGC(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		var before runtime.MemStats
		runtime.ReadMemStats(&before)

		runtime.GC()

		var after runtime.MemStats
		runtime.ReadMemStats(&after)

		freedMB := float64(before.Alloc-after.Alloc) / 1024 / 1024
		log.Printf("Periodic GC: freed %.2f MB, heap now %.2f MB", freedMB, float64(after.Alloc)/1024/1024)
	}
}

// logMemStats logs current memory statistics
func logMemStats() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	log.Printf("Memory stats: Alloc=%.2f MB, Sys=%.2f MB, NumGC=%d",
		float64(m.Alloc)/1024/1024,
		float64(m.Sys)/1024/1024,
		m.NumGC)
}
