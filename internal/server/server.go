package server

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	image "github.com/bevelgacom/wapipedia/pkg/wbmp"
	"github.com/bevelgacom/wapipedia/pkg/wikipedia"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"
)

// Global Wikipedia instance
var wiki *wikipedia.Wikipedia

// Random ID cache
const randomIDCacheSize = 100

var (
	randomIDCache  []uint32
	randomIDMutex  sync.Mutex
	randomIDRefill chan struct{}
)

// WikiHome represents the home page data
type WikiHome struct {
	ArticleCount uint32
	RandomID     uint32
}

// WikiSearch represents search results page data
type WikiSearch struct {
	Query        string
	QueryEncoded string
	Results      []wikipedia.SearchResult
	ShowMore     bool
	NextOffset   int
}

// WikiArticle represents article page data
type WikiArticle struct {
	Index          uint32
	Title          string
	Content        string
	ShowMore       bool
	NextPage       int
	HasInfobox     bool
	SupportsTables bool
}

// WikiInfobox represents infobox page data
type WikiInfobox struct {
	Index   uint32
	Title   string
	Content string
}

// WikiError represents error page data
type WikiError struct {
	Title   string
	Message string
}

// InitWikipedia initializes the Wikipedia reader with optional pre-built search index
func InitWikipedia(zimPath string) error {
	var err error
	// Use NewWikipediaWithIndex to load pre-built Bluge index if available
	wiki, err = wikipedia.NewWikipediaWithIndex(zimPath, "")
	if err != nil {
		return err
	}

	// Set global wiki reference for image ID lookups during HTML conversion
	wikipedia.SetGlobalWiki(wiki)

	// Initialize random ID cache
	initRandomIDCache()

	return nil
}

// initRandomIDCache initializes the random ID cache and starts the background refill goroutine
func initRandomIDCache() {
	log.Printf("Initializing random ID cache with size %d", randomIDCacheSize)
	randomIDCache = make([]uint32, 0, randomIDCacheSize)
	randomIDRefill = make(chan struct{}, randomIDCacheSize)

	// Pre-fill the cache
	log.Println("Starting initial random ID cache fill")
	go fillRandomIDCache(randomIDCacheSize)

	// Start background refill goroutine
	log.Println("Starting random ID refill worker")
	go randomIDRefillWorker()
}

// fillRandomIDCache fills the cache with random article IDs
func fillRandomIDCache(count int) {
	log.Printf("Filling random ID cache with %d entries", count)
	filled := 0
	for i := 0; i < count; i++ {
		if article, err := wiki.GetRandomArticle(); err == nil {
			randomIDMutex.Lock()
			randomIDCache = append(randomIDCache, article.Index)
			filled++
			log.Printf("Added random article ID %d to cache, new size: %d", article.Index, len(randomIDCache))
			randomIDMutex.Unlock()
		} else {
			log.Printf("Error getting random article for cache: %v", err)
		}
	}
	log.Printf("Filled %d random IDs, cache size now: %d", filled, len(randomIDCache))
}

// randomIDRefillWorker is a background goroutine that refills the cache when signaled
func randomIDRefillWorker() {
	log.Println("Random ID refill worker started")
	for range randomIDRefill {
		log.Printf("Refill signal received, cache size: %d", len(randomIDCache))
		fillRandomIDCache(1)
	}
}

// getRandomIDFromCache returns a random ID from the cache and triggers a refill
func getRandomIDFromCache() (uint32, bool) {
	randomIDMutex.Lock()
	defer randomIDMutex.Unlock()

	if len(randomIDCache) == 0 {
		log.Println("Random ID cache is empty")
		return 0, false
	}

	// Pop the last ID from the cache
	id := randomIDCache[len(randomIDCache)-1]
	randomIDCache = randomIDCache[:len(randomIDCache)-1]
	log.Printf("Got random ID %d from cache, remaining: %d", id, len(randomIDCache))

	// Signal the refill goroutine (non-blocking)
	select {
	case randomIDRefill <- struct{}{}:
		log.Println("Signaled refill worker")
	default:
		// Channel is full, refill already pending
		log.Println("Refill channel full, skipping signal")
	}

	return id, true
}

// serveWikiHome serves the Wikipedia home page
func serveWikiHome(c echo.Context) error {
	log.Printf("Serving home page, User-Agent: %s", c.Request().UserAgent())
	if wiki == nil {
		log.Println("Wiki not initialized, returning error")
		return serveWikiError(c, "Not Available", "Wikipedia data is not loaded. Please download a Wikipedia dump first.")
	}

	// Get a random article ID from the cache
	randomID := uint32(0)
	if id, ok := getRandomIDFromCache(); ok {
		randomID = id
	} else if article, err := wiki.GetRandomArticle(); err == nil {
		// Fallback if cache is empty
		log.Printf("Cache miss, fetched random article %d directly", article.Index)
		randomID = article.Index
	}

	data := WikiHome{
		RandomID: randomID,
	}

	tmpl := template.Must(template.ParseFiles("./static/home.wml"))
	c.Response().Header().Set("Content-Type", "text/vnd.wap.wml")
	return tmpl.Execute(c.Response().Writer, data)
}

// serveWikiSearch serves search results
func serveWikiSearch(c echo.Context) error {
	if wiki == nil {
		log.Println("Search request but wiki not initialized")
		return serveWikiError(c, "Not Available", "Wikipedia data is not loaded.")
	}

	query := c.QueryParam("q")
	log.Printf("Search request: q=%q, User-Agent: %s", query, c.Request().UserAgent())
	if query == "" {
		log.Println("Empty search query, redirecting to home")
		return c.Redirect(http.StatusFound, "/")
	}

	offset := 0
	if o := c.QueryParam("o"); o != "" {
		var err error
		offset, err = strconv.Atoi(o)
		if err != nil {
			offset = 0
		}
	}

	maxResults := 10
	log.Printf("Searching for %q with offset %d", query, offset)
	results, err := wiki.Search(query, maxResults+offset+1)
	if err != nil {
		log.Printf("Search error for %q: %v", query, err)
		return serveWikiError(c, "Search Error", "An error occurred while searching.")
	}
	log.Printf("Search for %q returned %d results", query, len(results))

	// Apply offset and limit
	showMore := false
	if offset < len(results) {
		if offset+maxResults < len(results) {
			showMore = true
			results = results[offset : offset+maxResults]
		} else {
			results = results[offset:]
		}
	} else {
		results = []wikipedia.SearchResult{}
	}

	// Escape titles for WML
	for i := range results {
		results[i].Title = wikipedia.FormatTitle(results[i].Title)
	}

	data := WikiSearch{
		Query:        escapeWMLAttr(query),
		QueryEncoded: url.QueryEscape(query),
		Results:      results,
		ShowMore:     showMore,
		NextOffset:   offset + maxResults,
	}

	tmpl := template.Must(template.ParseFiles("./static/search.wml"))
	c.Response().Header().Set("Content-Type", "text/vnd.wap.wml")
	return tmpl.Execute(c.Response().Writer, data)
}

// serveWikiArticle serves an article
func serveWikiArticle(c echo.Context) error {
	log.Printf("Article request: id=%s, p=%s, User-Agent: %s", c.QueryParam("id"), c.QueryParam("p"), c.Request().UserAgent())
	if wiki == nil {
		log.Println("Article request but wiki not initialized")
		return serveWikiError(c, "Not Available", "Wikipedia data is not loaded.")
	}

	idStr := c.QueryParam("id")
	if idStr == "" {
		log.Println("Article request with no ID")
		return serveWikiError(c, "Invalid Request", "No article ID specified.")
	}

	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return serveWikiError(c, "Invalid Request", "Invalid article ID.")
	}

	page := 0
	if p := c.QueryParam("p"); p != "" {
		page, err = strconv.Atoi(p)
		if err != nil {
			page = 0
		}
	}

	// Get render options based on device capabilities
	opts := getRenderOptions(c)
	log.Printf("Fetching article %d with options: SupportsTables=%v", id, opts.SupportsTables)
	article, err := wiki.GetArticleWithOptions(uint32(id), opts)
	if err != nil {
		log.Printf("Error getting article %d: %v", id, err)
		return serveWikiError(c, "Article Not Found", "The requested article could not be found.")
	}
	log.Printf("Serving article %d: %q, page %d", id, article.Title, page)

	// Check if article has an infobox (only show link on first page for non-Nokia 7110)
	hasInfobox := false
	if page == 0 && opts.SupportsTables {
		hasInfobox = wiki.HasInfobox(uint32(id))
	}

	// Split content for pagination
	maxContentLength := 800 // Characters per page
	chunks := wikipedia.SplitContent(article.Content, maxContentLength)

	showMore := false
	content := ""
	if page < len(chunks) {
		content = chunks[page]
		showMore = page+1 < len(chunks)
	} else if len(chunks) > 0 {
		content = chunks[len(chunks)-1]
	}

	data := WikiArticle{
		Index:          uint32(id),
		Title:          wikipedia.FormatTitle(article.Title),
		Content:        content,
		ShowMore:       showMore,
		NextPage:       page + 1,
		HasInfobox:     hasInfobox,
		SupportsTables: opts.SupportsTables,
	}

	tmpl := template.Must(template.ParseFiles("./static/article.wml"))
	c.Response().Header().Set("Content-Type", "text/vnd.wap.wml")
	return tmpl.Execute(c.Response().Writer, data)
}

// serveWikiInfobox serves an article's infobox as a WML table
func serveWikiInfobox(c echo.Context) error {
	if wiki == nil {
		return serveWikiError(c, "Not Available", "Wikipedia data is not loaded.")
	}

	// Check if device supports tables
	opts := getRenderOptions(c)
	if !opts.SupportsTables {
		return serveWikiError(c, "Not Supported", "Your device does not support tables.")
	}

	idStr := c.QueryParam("id")
	if idStr == "" {
		return serveWikiError(c, "Invalid Request", "No article ID specified.")
	}

	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return serveWikiError(c, "Invalid Request", "Invalid article ID.")
	}

	// Get the infobox content
	infobox, title, err := wiki.GetInfobox(uint32(id))
	if err != nil {
		log.Printf("Error getting infobox for article %d: %v", id, err)
		return serveWikiError(c, "No Infobox", "This article does not have an infobox.")
	}

	data := WikiInfobox{
		Index:   uint32(id),
		Title:   wikipedia.FormatTitle(title),
		Content: infobox,
	}

	tmpl := template.Must(template.ParseFiles("./static/infobox.wml"))
	c.Response().Header().Set("Content-Type", "text/vnd.wap.wml")
	return tmpl.Execute(c.Response().Writer, data)
}

// serveWikiRandom serves a random article
func serveWikiRandom(c echo.Context) error {
	if wiki == nil {
		return serveWikiError(c, "Not Available", "Wikipedia data is not loaded.")
	}

	article, err := wiki.GetRandomArticle()
	if err != nil {
		log.Printf("Error getting random article: %v", err)
		return serveWikiError(c, "Error", "Could not get a random article.")
	}

	// Serve the article directly (WAP gateways don't handle redirects well)
	opts := getRenderOptions(c)
	articleWithOpts, err := wiki.GetArticleWithOptions(article.Index, opts)
	if err != nil {
		return serveWikiError(c, "Error", "Could not load article.")
	}

	// Check for infobox
	hasInfobox := false
	if opts.SupportsTables {
		hasInfobox = wiki.HasInfobox(article.Index)
	}

	// Split content for pagination
	maxContentLength := 800
	chunks := wikipedia.SplitContent(articleWithOpts.Content, maxContentLength)

	content := ""
	showMore := false
	if len(chunks) > 0 {
		content = chunks[0]
		showMore = len(chunks) > 1
	}

	data := WikiArticle{
		Index:          article.Index,
		Title:          wikipedia.FormatTitle(articleWithOpts.Title),
		Content:        content,
		ShowMore:       showMore,
		NextPage:       1,
		HasInfobox:     hasInfobox,
		SupportsTables: opts.SupportsTables,
	}

	tmpl := template.Must(template.ParseFiles("./static/article.wml"))
	c.Response().Header().Set("Content-Type", "text/vnd.wap.wml")
	return tmpl.Execute(c.Response().Writer, data)
}

// serveWikiError serves an error page
func serveWikiError(c echo.Context, title, message string) error {
	data := WikiError{
		Title:   escapeWMLAttr(title),
		Message: escapeWMLAttr(message),
	}

	tmpl := template.Must(template.ParseFiles("./static/error.wml"))
	c.Response().Header().Set("Content-Type", "text/vnd.wap.wml")
	return tmpl.Execute(c.Response().Writer, data)
}

// serveWikiImage serves images from the ZIM file in JPEG or WBMP format
func serveWikiImage(c echo.Context) error {
	log.Printf("Image request: %s, Accept: %s", c.Param("*"), c.Request().Header.Get("Accept"))
	if wiki == nil {
		log.Println("Image request but wiki not initialized")
		return c.String(http.StatusServiceUnavailable, "Wikipedia data is not loaded.")
	}

	// Get image path from the URL parameter
	imagePath := c.Param("*")
	if imagePath == "" {
		log.Println("Image request with no path")
		return c.String(http.StatusBadRequest, "No image path specified.")
	}

	var content []byte
	var err error

	// Check if imagePath is a numeric ID
	if id, parseErr := strconv.ParseUint(imagePath, 10, 32); parseErr == nil {
		// Lookup by ID
		content, _, err = wiki.GetImageByID(uint32(id))
	} else {
		// Lookup by path (fallback for compatibility)
		content, _, err = wiki.GetImage(imagePath)
	}

	if err != nil {
		log.Printf("Error getting image %s: %v", imagePath, err)
		return c.String(http.StatusNotFound, "Image not found.")
	}

	// Check Accept header to determine output format
	accept := c.Request().Header.Get("Accept")

	if strings.Contains(accept, "image/jpeg") {
		log.Printf("Serving image %s as JPEG", imagePath)
		return c.Blob(http.StatusOK, "image/jpeg", image.ImageToJPEG(content, 80))
	}

	// Default to WBMP for WAP devices
	log.Printf("Serving image %s as WBMP", imagePath)
	return c.Blob(http.StatusOK, "image/vnd.wap.wbmp", image.ImageToWBMP(content, 80))
}

// RegisterWikiRoutes registers all Wikipedia-related routes
func RegisterWikiRoutes(e *echo.Echo) {
	// Add rate limiting middleware to prevent server overload
	// Allows 5 requests per second with a burst of 10 globally
	config := middleware.RateLimiterConfig{
		Skipper: middleware.DefaultSkipper,
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{
				Rate:      rate.Limit(5), // 5 requests per second
				Burst:     10,            // Allow bursts up to 10 requests
				ExpiresIn: 3 * time.Minute,
			},
		),
		IdentifierExtractor: func(ctx echo.Context) (string, error) {
			return "1", nil // no IP-based limiting, most of it comes from Kannel anyway
		},
		ErrorHandler: func(context echo.Context, err error) error {
			return context.String(http.StatusForbidden, "Rate limit error")
		},
		DenyHandler: func(context echo.Context, identifier string, err error) error {
			return context.String(http.StatusTooManyRequests, "Whelp we are a bit overloaded. Please try again later.")
		},
	}
	e.Use(middleware.RateLimiterWithConfig(config))

	e.GET("/", serveWikiHome)
	e.GET("/search", serveWikiSearch)
	e.GET("/article", serveWikiArticle)
	e.GET("/infobox", serveWikiInfobox)
	e.GET("/random", serveWikiRandom)
	e.GET("/image/*", serveWikiImage)
	e.GET("/wapipedia.wbmp", serveWAPipediaLogo)
}

// serveWAPipediaLogo serves the WAPipedia logo WBMP file
func serveWAPipediaLogo(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderContentType, "image/vnd.wap.wbmp")
	return c.File("./static/wapipedia.wbmp")
}

// GetZIMPath returns the path to the ZIM file from environment or default
func GetZIMPath() string {
	if path := os.Getenv("WAPIPEDIA_ZIM"); path != "" {
		return path
	}
	// Default location
	return "./data/wikipedia.zim"
}

// ServeHome redirects to wiki home
func ServeHome(c echo.Context) error {
	return c.Redirect(http.StatusFound, "/")
}
