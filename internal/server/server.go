package server

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/template"

	image "github.com/bevelgacom/wapipedia/pkg/wbmp"
	"github.com/bevelgacom/wapipedia/pkg/wikipedia"
	"github.com/labstack/echo/v4"
)

// Global Wikipedia instance
var wiki *wikipedia.Wikipedia

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

// InitWikipedia initializes the Wikipedia reader
func InitWikipedia(zimPath string) error {
	var err error
	wiki, err = wikipedia.NewWikipedia(zimPath)
	if err != nil {
		return err
	}

	// Set global wiki reference for image ID lookups during HTML conversion
	wikipedia.SetGlobalWiki(wiki)

	// Build index in background
	go func() {
		log.Println("Building Wikipedia search index...")
		if err := wiki.BuildIndex(); err != nil {
			log.Printf("Warning: Failed to build search index: %v", err)
		} else {
			log.Println("Wikipedia search index built successfully")
		}
	}()

	return nil
}

// serveWikiHome serves the Wikipedia home page
func serveWikiHome(c echo.Context) error {
	if wiki == nil {
		return serveWikiError(c, "Not Available", "Wikipedia data is not loaded. Please download a Wikipedia dump first.")
	}

	// Get a random article ID for the random link
	randomID := uint32(0)
	if article, err := wiki.GetRandomArticle(); err == nil {
		randomID = article.Index
	}

	data := WikiHome{
		ArticleCount: wiki.GetArticleCount(),
		RandomID:     randomID,
	}

	tmpl := template.Must(template.ParseFiles("./static/home.wml"))
	c.Response().Header().Set("Content-Type", "text/vnd.wap.wml")
	return tmpl.Execute(c.Response().Writer, data)
}

// serveWikiSearch serves search results
func serveWikiSearch(c echo.Context) error {
	if wiki == nil {
		return serveWikiError(c, "Not Available", "Wikipedia data is not loaded.")
	}

	query := c.QueryParam("q")
	if query == "" {
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
	results, err := wiki.Search(query, maxResults+offset+1)
	if err != nil {
		log.Printf("Search error: %v", err)
		return serveWikiError(c, "Search Error", "An error occurred while searching.")
	}

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
	if wiki == nil {
		return serveWikiError(c, "Not Available", "Wikipedia data is not loaded.")
	}

	idStr := c.QueryParam("id")
	if idStr == "" {
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
	article, err := wiki.GetArticleWithOptions(uint32(id), opts)
	if err != nil {
		log.Printf("Error getting article %d: %v", id, err)
		return serveWikiError(c, "Article Not Found", "The requested article could not be found.")
	}

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
	if wiki == nil {
		return c.String(http.StatusServiceUnavailable, "Wikipedia data is not loaded.")
	}

	// Get image path from the URL parameter
	imagePath := c.Param("*")
	if imagePath == "" {
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
		return c.Blob(http.StatusOK, "image/jpeg", image.ImageToJPEG(content, 80))
	}

	// Default to WBMP for WAP devices
	return c.Blob(http.StatusOK, "image/vnd.wap.wbmp", image.ImageToWBMP(content, 80))
}

// RegisterWikiRoutes registers all Wikipedia-related routes
func RegisterWikiRoutes(e *echo.Echo) {
	e.GET("/", serveWikiHome)
	e.GET("/search", serveWikiSearch)
	e.GET("/article", serveWikiArticle)
	e.GET("/infobox", serveWikiInfobox)
	e.GET("/random", serveWikiRandom)
	e.GET("/image/*", serveWikiImage)
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
