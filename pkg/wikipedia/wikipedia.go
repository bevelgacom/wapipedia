package wikipedia

import (
	"errors"
	"fmt"
	"html"
	"math/rand"
	"net/url"
	"regexp"
	"strings"
)

// Article represents a Wikipedia article
type Article struct {
	Index   uint32
	URL     string
	Title   string
	Content string
}

// RenderOptions controls how HTML is converted to WML
type RenderOptions struct {
	SupportsTables bool // Whether the device supports WML tables
}

// SearchResult represents a search result
type SearchResult struct {
	Index uint32
	URL   string
	Title string
	Score float64
}

// Wikipedia handles Wikipedia content from ZIM files
type Wikipedia struct {
	zimPath      string
	reader       *ZIMReader
	blugeIndex   *BlugeIndex // persistent Bluge search index
	articleCount uint32      // count of actual articles
}

// NewWikipedia creates a new Wikipedia instance
func NewWikipedia(zimPath string) (*Wikipedia, error) {
	reader, err := NewZIMReader(zimPath)
	if err != nil {
		return nil, err
	}

	w := &Wikipedia{
		zimPath: zimPath,
		reader:  reader,
	}

	return w, nil
}

// NewWikipediaWithIndex creates a new Wikipedia instance and loads the Bluge index
func NewWikipediaWithIndex(zimPath, indexPath string) (*Wikipedia, error) {
	w, err := NewWikipedia(zimPath)
	if err != nil {
		return nil, err
	}

	// Try to load Bluge index
	if indexPath == "" {
		indexPath = DefaultIndexPath(zimPath)
	}

	blugeIndex, err := LoadBlugeIndex(indexPath)
	if err != nil {
		// Index not available, search won't work
		fmt.Printf("Warning: Search index not found at %s\n", indexPath)
		fmt.Printf("Run 'wapipedia index -z %s' to build the search index.\n", zimPath)
	} else {
		w.blugeIndex = blugeIndex
		if count, err := blugeIndex.GetDocumentCount(); err == nil {
			w.articleCount = uint32(count)
			fmt.Printf("Loaded search index with %d articles\n", count)
		}
	}

	return w, nil
}

// Close closes the Wikipedia reader
func (w *Wikipedia) Close() error {
	if w.blugeIndex != nil {
		w.blugeIndex.Close()
	}
	if w.reader != nil {
		return w.reader.Close()
	}
	return nil
}

// Search searches for articles matching the query using Bluge index
func (w *Wikipedia) Search(query string, maxResults int) ([]SearchResult, error) {
	if w.blugeIndex == nil {
		return nil, errors.New("search index not loaded - run 'wapipedia index' first")
	}
	return w.blugeIndex.Search(query, maxResults)
}

// GetArticle retrieves an article by its index
func (w *Wikipedia) GetArticle(idx uint32) (*Article, error) {
	return w.GetArticleWithOptions(idx, RenderOptions{SupportsTables: true})
}

// GetArticleWithOptions retrieves an article with specific rendering options
func (w *Wikipedia) GetArticleWithOptions(idx uint32, opts RenderOptions) (*Article, error) {
	return w.getArticleWithRedirectDepth(idx, 0, opts)
}

// getArticleWithRedirectDepth retrieves an article, following HTML redirects up to maxDepth
func (w *Wikipedia) getArticleWithRedirectDepth(idx uint32, depth int, opts RenderOptions) (*Article, error) {
	if depth > 5 {
		return nil, errors.New("too many redirects")
	}

	entry, err := w.reader.GetDirectoryEntry(idx)
	if err != nil {
		return nil, err
	}

	content, _, err := w.reader.GetArticleContent(idx)
	if err != nil {
		return nil, err
	}

	htmlContent := string(content)

	// Check if this is an HTML redirect page and follow it
	if strings.Contains(htmlContent, `http-equiv="refresh"`) {
		reRefresh := regexp.MustCompile(`content="[^"]*URL='([^']*)'`)
		matches := reRefresh.FindStringSubmatch(htmlContent)
		if len(matches) > 1 {
			target := matches[1]
			// Clean up the target URL
			target = strings.TrimPrefix(target, "./")
			// Remove fragment/anchor
			if idx := strings.Index(target, "#"); idx != -1 {
				target = target[:idx]
			}
			// Try to find and return the target article
			targetArticle, err := w.GetArticleByURL(target)
			if err == nil {
				return targetArticle, nil
			}
			// If we can't find it, fall through to show the redirect message
		}
	}

	// Convert HTML to WML
	wmlContent := HTMLToWMLWithOptions(htmlContent, opts)

	// Remove the article title from the beginning of content (it's shown in card title)
	wmlContent = stripLeadingTitle(wmlContent, entry.Title)

	return &Article{
		Index:   idx,
		URL:     entry.URL,
		Title:   entry.Title,
		Content: wmlContent,
	}, nil
}

// GetArticleByURL retrieves an article by its URL
func (w *Wikipedia) GetArticleByURL(url string) (*Article, error) {
	idx, err := w.reader.FindArticleByURL('A', url)
	if err != nil {
		// Try with 'C' namespace (for some ZIM files)
		idx, err = w.reader.FindArticleByURL('C', url)
		if err != nil {
			return nil, err
		}
	}
	return w.GetArticle(idx)
}

// GetImage retrieves an image from the ZIM file by its path
func (w *Wikipedia) GetImage(path string) ([]byte, string, error) {
	// Images in ZIM files can be in namespace 'I' (images) or '-' (other resources)
	// Try 'I' namespace first (traditional), then '-'
	idx, err := w.reader.FindArticleByURL('I', path)
	if err != nil {
		idx, err = w.reader.FindArticleByURL('-', path)
		if err != nil {
			// Also try with 'C' namespace for content
			idx, err = w.reader.FindArticleByURL('C', path)
			if err != nil {
				return nil, "", fmt.Errorf("image not found: %s", path)
			}
		}
	}

	content, mimeType, err := w.reader.GetArticleContent(idx)
	if err != nil {
		return nil, "", err
	}

	return content, mimeType, nil
}

// GetImageByID retrieves an image from the ZIM file by its index ID
func (w *Wikipedia) GetImageByID(idx uint32) ([]byte, string, error) {
	content, mimeType, err := w.reader.GetArticleContent(idx)
	if err != nil {
		return nil, "", err
	}
	return content, mimeType, nil
}

// FindImageID finds the ZIM index for an image by its path
func (w *Wikipedia) FindImageID(path string) (uint32, error) {
	// Try URL-decoded path first (ZIM stores decoded URLs)
	decodedPath, _ := url.QueryUnescape(path)

	// Try with decoded path first
	for _, tryPath := range []string{decodedPath, path} {
		if tryPath == "" {
			continue
		}
		// Images in ZIM files can be in namespace 'I' (images) or '-' (other resources)
		idx, err := w.reader.FindArticleByURL('I', tryPath)
		if err == nil {
			return idx, nil
		}
		idx, err = w.reader.FindArticleByURL('-', tryPath)
		if err == nil {
			return idx, nil
		}
		idx, err = w.reader.FindArticleByURL('C', tryPath)
		if err == nil {
			return idx, nil
		}
	}
	return 0, fmt.Errorf("image not found: %s", path)
}

// HasInfobox checks if an article has an infobox
func (w *Wikipedia) HasInfobox(idx uint32) bool {
	content, _, err := w.reader.GetArticleContent(idx)
	if err != nil {
		return false
	}
	htmlContent := string(content)
	return strings.Contains(htmlContent, `class="infobox`) ||
		strings.Contains(htmlContent, `class="infobox"`)
}

// GetInfobox extracts and formats the infobox from an article as WML tables
func (w *Wikipedia) GetInfobox(idx uint32) (string, string, error) {
	entry, err := w.reader.GetDirectoryEntry(idx)
	if err != nil {
		return "", "", err
	}

	content, _, err := w.reader.GetArticleContent(idx)
	if err != nil {
		return "", "", err
	}

	htmlContent := string(content)

	// Extract infobox table
	reInfobox := regexp.MustCompile(`(?is)<table[^>]*class="[^"]*infobox[^"]*"[^>]*>(.*?)</table>`)
	match := reInfobox.FindStringSubmatch(htmlContent)
	if len(match) < 2 {
		return "", "", errors.New("no infobox found")
	}

	infoboxHTML := match[1]

	// Convert to WML table format
	wmlContent := convertInfoboxToWML(infoboxHTML)

	return wmlContent, entry.Title, nil
}

// convertInfoboxToWML converts infobox HTML to WML table format
func convertInfoboxToWML(infoboxHTML string) string {
	var result strings.Builder

	// Find all rows
	reRow := regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	rows := reRow.FindAllStringSubmatch(infoboxHTML, -1)

	if len(rows) == 0 {
		return ""
	}

	result.WriteString("<table columns=\"2\">\n")

	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		rowContent := row[1]

		// Extract header (th) and data (td) cells
		reTh := regexp.MustCompile(`(?is)<th[^>]*>(.*?)</th>`)
		reTd := regexp.MustCompile(`(?is)<td[^>]*>(.*?)</td>`)

		thMatches := reTh.FindAllStringSubmatch(rowContent, -1)
		tdMatches := reTd.FindAllStringSubmatch(rowContent, -1)

		// Skip rows with no data
		if len(thMatches) == 0 && len(tdMatches) == 0 {
			continue
		}

		// Clean and extract cell text
		var cells []string
		for _, th := range thMatches {
			if len(th) > 1 {
				cells = append(cells, cleanCellContent(th[1]))
			}
		}
		for _, td := range tdMatches {
			if len(td) > 1 {
				cells = append(cells, cleanCellContent(td[1]))
			}
		}

		// Skip empty rows
		hasContent := false
		for _, cell := range cells {
			if strings.TrimSpace(cell) != "" {
				hasContent = true
				break
			}
		}
		if !hasContent {
			continue
		}

		// Build row with 2 columns
		result.WriteString("<tr>")
		if len(cells) >= 2 {
			result.WriteString(fmt.Sprintf("<td>%s</td><td>%s</td>", cells[0], cells[1]))
		} else if len(cells) == 1 {
			result.WriteString(fmt.Sprintf("<td>%s</td><td></td>", cells[0]))
		}
		result.WriteString("</tr>\n")
	}

	result.WriteString("</table>")
	return result.String()
}

// cleanCellContent strips HTML and cleans up cell content for WML
func cleanCellContent(content string) string {
	// Remove nested HTML tags
	reTags := regexp.MustCompile(`<[^>]+>`)
	content = reTags.ReplaceAllString(content, " ")

	// Decode HTML entities
	content = html.UnescapeString(content)

	// Clean up whitespace
	reSpaces := regexp.MustCompile(`\s+`)
	content = reSpaces.ReplaceAllString(content, " ")
	content = strings.TrimSpace(content)

	// Escape for WML
	content = escapeWML(content)

	// Truncate very long content
	if len(content) > 100 {
		content = content[:97] + "..."
	}

	return content
}

// GetRandomArticle returns a random article
func (w *Wikipedia) GetRandomArticle() (*Article, error) {
	articleCount := w.reader.GetArticleCount()
	if articleCount == 0 {
		return nil, errors.New("no articles available")
	}

	// Try to find a valid HTML article (namespace A or C, not redirect, HTML content)
	maxAttempts := 500
	for i := 0; i < maxAttempts; i++ {
		idx := uint32(rand.Int63n(int64(articleCount)))
		entry, err := w.reader.GetDirectoryEntry(idx)
		if err != nil {
			continue
		}

		// Must be in article namespace and not a redirect
		if entry.Namespace != 'A' && entry.Namespace != 'C' {
			continue
		}
		if entry.IsRedirect {
			continue
		}

		// Check if it's an HTML article by looking at the URL and content
		url := strings.ToLower(entry.URL)
		// Skip resource files
		if strings.HasSuffix(url, ".css") || strings.HasSuffix(url, ".js") ||
			strings.HasSuffix(url, ".png") || strings.HasSuffix(url, ".jpg") ||
			strings.HasSuffix(url, ".jpeg") || strings.HasSuffix(url, ".gif") ||
			strings.HasSuffix(url, ".svg") || strings.HasSuffix(url, ".ico") ||
			strings.HasSuffix(url, ".woff") || strings.HasSuffix(url, ".woff2") ||
			strings.HasSuffix(url, ".ttf") || strings.HasSuffix(url, ".eot") ||
			strings.Contains(url, "/-/") {
			continue
		}

		// Verify it's actually HTML by checking content
		content, mimeType, err := w.reader.GetArticleContent(idx)
		if err != nil {
			continue
		}

		// Check MIME type if available
		if mimeType != "" && !strings.Contains(mimeType, "html") && !strings.Contains(mimeType, "text") {
			continue
		}

		// Check content starts with HTML-like content
		contentStr := string(content)
		if len(contentStr) < 50 {
			continue
		}
		// Look for HTML indicators
		if !strings.Contains(contentStr[:min(500, len(contentStr))], "<") {
			continue
		}

		return w.GetArticle(idx)
	}

	return nil, errors.New("could not find a valid random article")
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GetArticleCount returns the number of articles from the search index
// If index not loaded, returns an estimate based on ZIM header
func (w *Wikipedia) GetArticleCount() uint32 {
	if w.articleCount > 0 {
		return w.articleCount
	}
	// Return ZIM header count as estimate (includes all entries, not just articles)
	// Typical Wikipedia ZIM files have ~50% actual articles
	return w.reader.GetArticleCount() / 2
}

// HTMLToWML converts HTML content to WML-safe plain text (no tables)
func HTMLToWML(htmlContent string) string {
	return HTMLToWMLWithOptions(htmlContent, RenderOptions{SupportsTables: false})
}

// HTMLToWMLWithOptions converts HTML content to WML with configurable options
func HTMLToWMLWithOptions(htmlContent string, opts RenderOptions) string {
	// Check if this is an HTML redirect page
	if strings.Contains(htmlContent, `http-equiv="refresh"`) {
		// Extract the redirect target
		reRefresh := regexp.MustCompile(`content="[^"]*URL='([^']*)'`)
		matches := reRefresh.FindStringSubmatch(htmlContent)
		if len(matches) > 1 {
			target := matches[1]
			// Clean up the target URL
			target = strings.TrimPrefix(target, "./")
			target = strings.ReplaceAll(target, "#", " - section: ")
			return fmt.Sprintf("This article redirects to: %s\n\nPlease search for the target article.", escapeWML(target))
		}
	}

	// Remove script and style tags
	reScript := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	content := reScript.ReplaceAllString(htmlContent, "")
	content = reStyle.ReplaceAllString(content, "")

	// Remove HTML comments
	reComment := regexp.MustCompile(`<!--.*?-->`)
	content = reComment.ReplaceAllString(content, "")

	// Remove infoboxes and navigation boxes (they don't render well in WML)
	reInfobox := regexp.MustCompile(`(?is)<table[^>]*class="[^"]*infobox[^"]*"[^>]*>.*?</table>`)
	content = reInfobox.ReplaceAllString(content, "")
	reNavbox := regexp.MustCompile(`(?is)<table[^>]*class="[^"]*navbox[^"]*"[^>]*>.*?</table>`)
	content = reNavbox.ReplaceAllString(content, "")
	reAmbox := regexp.MustCompile(`(?is)<table[^>]*class="[^"]*ambox[^"]*"[^>]*>.*?</table>`)
	content = reAmbox.ReplaceAllString(content, "")

	// Remove table of contents
	reToc := regexp.MustCompile(`(?is)<div[^>]*id="toc"[^>]*>.*?</div>`)
	content = reToc.ReplaceAllString(content, "")

	// Remove the page title (h1 with id="firstHeading" or class="firstHeading")
	// This is already shown in the WML card title
	reFirstHeading := regexp.MustCompile(`(?is)<h1[^>]*(?:id|class)="[^"]*firstHeading[^"]*"[^>]*>.*?</h1>`)
	content = reFirstHeading.ReplaceAllString(content, "")

	// Remove the mw-page-title-main span (Wikipedia page title)
	rePageTitle := regexp.MustCompile(`(?is)<span[^>]*class="[^"]*mw-page-title-main[^"]*"[^>]*>.*?</span>`)
	content = rePageTitle.ReplaceAllString(content, "")

	// Remove the header div that contains the title
	reHeader := regexp.MustCompile(`(?is)<header[^>]*>.*?</header>`)
	content = reHeader.ReplaceAllString(content, "")

	// Remove mw-body-content header section with title duplicate
	reBodyHeader := regexp.MustCompile(`(?is)<div[^>]*class="[^"]*mw-body-content[^"]*"[^>]*>\s*<div[^>]*class="[^"]*mw-content-container[^"]*"[^>]*>`)
	content = reBodyHeader.ReplaceAllString(content, "")

	// Remove article title at start (it's already in the card title)
	reArticleTitle := regexp.MustCompile(`(?is)^\s*<[^>]*>?[^<]*</[^>]*>\s*`)
	content = reArticleTitle.ReplaceAllString(content, "")

	// Remove reference sections and citations
	reRef := regexp.MustCompile(`(?is)<sup[^>]*class="[^"]*reference[^"]*"[^>]*>.*?</sup>`)
	content = reRef.ReplaceAllString(content, "")

	// Convert images to WML img tags pointing to /image/ endpoint
	content = convertHTMLImagesToWML(content)

	// Convert HTML links to WML anchors
	content = convertHTMLLinksToWML(content)

	// Convert article tables to text with line breaks
	// Only infoboxes (which are handled separately via GetInfobox) use WML tables
	content = convertHTMLTablesToText(content)

	// Convert HTML formatting to WML formatting elements
	// Bold: <b>, <strong>
	reBold := regexp.MustCompile(`(?i)<(b|strong)[^>]*>(.*?)</(b|strong)>`)
	content = reBold.ReplaceAllString(content, "<b>$2</b>")

	// Italic: <i>, <em>, <cite>
	reItalic := regexp.MustCompile(`(?i)<(i|em|cite)[^>]*>(.*?)</(i|em|cite)>`)
	content = reItalic.ReplaceAllString(content, "<i>$2</i>")

	// Remove nested formatting tags (WML doesn't support nested <i>, <b>, etc.)
	content = removeNestedFormattingTags(content)

	// Underline
	reUnderline := regexp.MustCompile(`(?i)<u[^>]*>(.*?)</u>`)
	content = reUnderline.ReplaceAllString(content, "<u>$1</u>")

	// Big text
	reBig := regexp.MustCompile(`(?i)<big[^>]*>(.*?)</big>`)
	content = reBig.ReplaceAllString(content, "<big>$1</big>")

	// Small text
	reSmall := regexp.MustCompile(`(?i)<small[^>]*>(.*?)</small>`)
	content = reSmall.ReplaceAllString(content, "<small>$1</small>")

	// Convert headings to bold with line breaks
	reH1 := regexp.MustCompile(`(?i)<h1[^>]*>(.*?)</h1>`)
	content = reH1.ReplaceAllString(content, "<br/><br/><b>$1</b><br/>")
	reH2 := regexp.MustCompile(`(?i)<h2[^>]*>(.*?)</h2>`)
	content = reH2.ReplaceAllString(content, "<br/><br/><b>$1</b><br/>")
	reH3 := regexp.MustCompile(`(?i)<h3[^>]*>(.*?)</h3>`)
	content = reH3.ReplaceAllString(content, "<br/><br/><b>$1</b><br/>")
	reH456 := regexp.MustCompile(`(?i)<h[4-6][^>]*>(.*?)</h[4-6]>`)
	content = reH456.ReplaceAllString(content, "<br/><br/><b>$1</b><br/>")

	// Paragraphs to breaks
	reOpenP := regexp.MustCompile(`(?i)<p[^>]*>`)
	content = reOpenP.ReplaceAllString(content, "<br/><br/>")
	reCloseP := regexp.MustCompile(`(?i)</p>`)
	content = reCloseP.ReplaceAllString(content, "<br/>")

	// Line breaks - normalize to WML format
	reBr := regexp.MustCompile(`(?i)<br\s*/?>`)
	content = reBr.ReplaceAllString(content, "<br/>")

	// Divs to breaks
	reDiv := regexp.MustCompile(`(?i)</?div[^>]*>`)
	content = reDiv.ReplaceAllString(content, "<br/>")

	// List items with bullet
	reLi := regexp.MustCompile(`(?i)<li[^>]*>\s*`)
	content = reLi.ReplaceAllString(content, "<br/>• ")
	reEndLi := regexp.MustCompile(`(?i)</li>`)
	content = reEndLi.ReplaceAllString(content, "")

	// Remove ul/ol tags
	reList := regexp.MustCompile(`(?i)</?[ou]l[^>]*>`)
	content = reList.ReplaceAllString(content, "<br/>")

	// Remove all remaining HTML tags but preserve WML formatting and table tags
	// Use placeholders to protect WML tags during stripping
	wmlTagPlaceholders := map[string]string{
		"<b>":      "%%WMLB%%",
		"</b>":     "%%WMLBC%%",
		"<i>":      "%%WMLI%%",
		"</i>":     "%%WMLIC%%",
		"<u>":      "%%WMLU%%",
		"</u>":     "%%WMLUC%%",
		"<big>":    "%%WMLBIG%%",
		"</big>":   "%%WMLBIGC%%",
		"<small>":  "%%WMLSMALL%%",
		"</small>": "%%WMLSMALLC%%",
		"<br/>":    "%%WMLBR%%",
		"</a>":     "%%WMLAC%%",
	}

	// Protect WML formatting tags
	for tag, placeholder := range wmlTagPlaceholders {
		content = strings.ReplaceAll(content, tag, placeholder)
	}

	// Protect WML img tags (they have dynamic src attributes)
	reWMLImg := regexp.MustCompile(`<img src="/image/[^"]*" alt="[^"]*"/>`)
	imgMatches := reWMLImg.FindAllString(content, -1)
	for i, img := range imgMatches {
		content = strings.Replace(content, img, fmt.Sprintf("%%WMLIMG%d%%", i), 1)
	}

	// Protect WML anchor tags (they have dynamic href attributes)
	reWMLAnchor := regexp.MustCompile(`<a href="/article\?id=\d+">`)
	anchorMatches := reWMLAnchor.FindAllString(content, -1)
	for i, anchor := range anchorMatches {
		content = strings.Replace(content, anchor, fmt.Sprintf("%%WMLANCHOR%d%%", i), 1)
	}

	// Strip all remaining HTML tags
	reTags := regexp.MustCompile(`<[^>]+>`)
	content = reTags.ReplaceAllString(content, "")

	// Restore WML formatting tags
	for tag, placeholder := range wmlTagPlaceholders {
		content = strings.ReplaceAll(content, placeholder, tag)
	}

	// Restore WML img tags
	for i, img := range imgMatches {
		content = strings.Replace(content, fmt.Sprintf("%%WMLIMG%d%%", i), img, 1)
	}

	// Restore WML anchor tags
	for i, anchor := range anchorMatches {
		content = strings.Replace(content, fmt.Sprintf("%%WMLANCHOR%d%%", i), anchor, 1)
	}

	// Decode HTML entities
	content = html.UnescapeString(content)

	// Clean up whitespace between tags
	reSpaceBetweenBr := regexp.MustCompile(`<br/>\s*<br/>`)
	for i := 0; i < 5; i++ { // Multiple passes to catch nested cases
		content = reSpaceBetweenBr.ReplaceAllString(content, "<br/><br/>")
	}

	// Remove empty bullets (• with only whitespace after)
	reEmptyBullet := regexp.MustCompile(`<br/>•\s*(<br/>|$)`)
	for i := 0; i < 5; i++ {
		content = reEmptyBullet.ReplaceAllString(content, "<br/>")
	}

	// Clean up multiple consecutive breaks (more than 2)
	reMultiBr := regexp.MustCompile(`(<br/>){3,}`)
	content = reMultiBr.ReplaceAllString(content, "<br/><br/>")

	// Clean up whitespace
	reMultiSpace := regexp.MustCompile(`[ \t]+`)
	content = reMultiSpace.ReplaceAllString(content, " ")

	// Remove whitespace around breaks
	content = regexp.MustCompile(`\s*<br/>\s*`).ReplaceAllString(content, "<br/>")

	// Re-add single space after breaks for readability but not multiple
	reMultiBr2 := regexp.MustCompile(`(<br/>){3,}`)
	content = reMultiBr2.ReplaceAllString(content, "<br/><br/>")

	// Remove breaks at start
	for strings.HasPrefix(content, "<br/>") {
		content = strings.TrimPrefix(content, "<br/>")
	}

	content = strings.TrimSpace(content)

	// Escape special WML characters (preserving WML formatting tags)
	content = escapeWMLPreserveTags(content)

	return content
}

// Global wiki instance reference for image ID lookup during conversion
var globalWiki *Wikipedia

// SetGlobalWiki sets the global Wikipedia instance for image lookups
func SetGlobalWiki(w *Wikipedia) {
	globalWiki = w
}

// convertHTMLImagesToWML converts HTML img tags to WML img tags pointing to /image/ endpoint
func convertHTMLImagesToWML(content string) string {
	// Match img tags with src attribute
	reImg := regexp.MustCompile(`(?i)<img[^>]*src=["']([^"']+)["'][^>]*>`)

	content = reImg.ReplaceAllStringFunc(content, func(imgTag string) string {
		// Extract src
		srcMatch := regexp.MustCompile(`(?i)src=["']([^"']+)["']`).FindStringSubmatch(imgTag)
		if len(srcMatch) < 2 {
			return ""
		}
		src := srcMatch[1]

		// Skip non-image files, SVGs (not supported in WBMP), and data URIs
		srcLower := strings.ToLower(src)
		if strings.HasPrefix(srcLower, "data:") {
			return ""
		}
		if strings.HasSuffix(srcLower, ".svg") {
			return "" // SVG can't be converted to WBMP
		}

		// Extract alt text if available
		alt := "image"
		altMatch := regexp.MustCompile(`(?i)alt=["']([^"']+)["']`).FindStringSubmatch(imgTag)
		if len(altMatch) > 1 {
			alt = altMatch[1]
			// Truncate long alt text
			if len(alt) > 20 {
				alt = alt[:17] + "..."
			}
			// Escape for WML attribute
			alt = strings.ReplaceAll(alt, "\"", "&quot;")
			alt = strings.ReplaceAll(alt, "'", "&apos;")
		}

		// Convert src to /image/ endpoint path
		// Handle relative paths like ./path or ../path
		src = strings.TrimPrefix(src, "./")
		src = strings.TrimPrefix(src, "../")

		// Handle absolute paths starting with /
		if strings.HasPrefix(src, "/") {
			src = src[1:]
		}

		// Try to find image ID for shorter URLs
		if globalWiki != nil {
			if imgID, err := globalWiki.FindImageID(src); err == nil {
				return fmt.Sprintf(`<br/><img src="/image/%d" alt="%s"/><br/>`, imgID, alt)
			}
		}

		// Fallback to path-based URL if ID lookup fails
		return fmt.Sprintf(`<br/><img src="/image/%s" alt="%s"/><br/>`, src, alt)
	})

	return content
}

// convertHTMLLinksToWML converts HTML anchor tags to WML anchors with article IDs
func convertHTMLLinksToWML(content string) string {
	// First, handle anchor tags with href attribute
	reAnchor := regexp.MustCompile(`(?is)<a\s[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)

	content = reAnchor.ReplaceAllStringFunc(content, func(anchorTag string) string {
		// Extract href
		hrefMatch := regexp.MustCompile(`(?i)href=["']([^"']+)["']`).FindStringSubmatch(anchorTag)
		if len(hrefMatch) < 2 {
			// No href, just return the text content
			textMatch := regexp.MustCompile(`(?is)<a[^>]*>(.*?)</a>`).FindStringSubmatch(anchorTag)
			if len(textMatch) > 1 {
				return textMatch[1]
			}
			return ""
		}
		href := hrefMatch[1]

		// Extract link text
		textMatch := regexp.MustCompile(`(?is)<a[^>]*>(.*?)</a>`).FindStringSubmatch(anchorTag)
		linkText := ""
		if len(textMatch) > 1 {
			// Strip any nested HTML tags from link text
			linkText = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(textMatch[1], "")
			linkText = strings.TrimSpace(linkText)
		}
		if linkText == "" {
			return ""
		}

		// Skip external links, anchors, and special links
		if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") ||
			strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") ||
			strings.HasPrefix(href, "javascript:") {
			// Return just the text for external/special links
			return linkText
		}

		// Handle internal Wikipedia links
		// Clean up the href
		href = strings.TrimPrefix(href, "./")
		href = strings.TrimPrefix(href, "../")
		if strings.HasPrefix(href, "/") {
			href = href[1:]
		}

		// Remove fragment/anchor from URL
		if idx := strings.Index(href, "#"); idx != -1 {
			href = href[:idx]
		}

		// URL decode the href
		decodedHref, _ := url.QueryUnescape(href)
		if decodedHref != "" {
			href = decodedHref
		}

		// Skip non-article links (files, special pages, etc.)
		hrefLower := strings.ToLower(href)
		if strings.HasPrefix(hrefLower, "file:") || strings.HasPrefix(hrefLower, "special:") ||
			strings.HasPrefix(hrefLower, "wikipedia:") || strings.HasPrefix(hrefLower, "help:") ||
			strings.HasPrefix(hrefLower, "template:") || strings.HasPrefix(hrefLower, "category:") ||
			strings.HasPrefix(hrefLower, "talk:") || strings.HasPrefix(hrefLower, "user:") {
			return linkText
		}

		// Try to find article ID
		if globalWiki != nil {
			if idx, err := globalWiki.reader.FindArticleByURL('A', href); err == nil {
				return fmt.Sprintf(`<a href="/article?id=%d">%s</a>`, idx, escapeWML(linkText))
			}
			// Try C namespace
			if idx, err := globalWiki.reader.FindArticleByURL('C', href); err == nil {
				return fmt.Sprintf(`<a href="/article?id=%d">%s</a>`, idx, escapeWML(linkText))
			}
		}

		// If article not found, just return the text
		return linkText
	})

	// Clean up any remaining anchor tags without href (like <a name="...">)
	reAnchorNoHref := regexp.MustCompile(`(?is)<a\s[^>]*>(.*?)</a>`)
	content = reAnchorNoHref.ReplaceAllString(content, "$1")

	// Remove any stray closing </a> tags that might remain
	content = strings.ReplaceAll(content, "</a>", "")

	return content
}

// convertHTMLTablesToText converts HTML tables to plain text with line breaks
// This is used for article content (not infoboxes) to make tables readable without WML table support
func convertHTMLTablesToText(content string) string {
	// Find and convert each table to text
	reTable := regexp.MustCompile(`(?is)<table[^>]*>(.*?)</table>`)

	content = reTable.ReplaceAllStringFunc(content, func(tableHTML string) string {
		// Extract table content
		reTableInner := regexp.MustCompile(`(?is)<table[^>]*>(.*?)</table>`)
		innerMatch := reTableInner.FindStringSubmatch(tableHTML)
		if len(innerMatch) < 2 {
			return ""
		}
		tableContent := innerMatch[1]

		var result strings.Builder
		result.WriteString("<br/>")

		// Process rows
		reRow := regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
		rows := reRow.FindAllStringSubmatch(tableContent, -1)

		for _, row := range rows {
			if len(row) < 2 {
				continue
			}

			// Extract cells (both th and td)
			reCellContent := regexp.MustCompile(`(?is)<t[hd][^>]*>(.*?)</t[hd]>`)
			cells := reCellContent.FindAllStringSubmatch(row[1], -1)

			var cellTexts []string
			for _, cell := range cells {
				if len(cell) > 1 {
					// Strip any nested HTML from cell content
					cellText := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(cell[1], "")
					cellText = strings.TrimSpace(cellText)
					cellText = html.UnescapeString(cellText)
					if cellText != "" {
						cellTexts = append(cellTexts, cellText)
					}
				}
			}

			// Join cells with separator and add line break
			if len(cellTexts) > 0 {
				result.WriteString(strings.Join(cellTexts, " - "))
				result.WriteString("<br/>")
			}
		}

		return result.String()
	})

	return content
}

// escapeWMLPreserveTags escapes WML special chars but preserves WML formatting tags
func escapeWMLPreserveTags(s string) string {
	// All WML tags to preserve (no tables - article tables are converted to text)
	wmlTags := map[string]string{
		"<b>":       "%%B_OPEN%%",
		"</b>":      "%%B_CLOSE%%",
		"<i>":       "%%I_OPEN%%",
		"</i>":      "%%I_CLOSE%%",
		"<u>":       "%%U_OPEN%%",
		"</u>":      "%%U_CLOSE%%",
		"<big>":     "%%BIG_OPEN%%",
		"</big>":    "%%BIG_CLOSE%%",
		"<small>":   "%%SMALL_OPEN%%",
		"</small>":  "%%SMALL_CLOSE%%",
		"<em>":      "%%EM_OPEN%%",
		"</em>":     "%%EM_CLOSE%%",
		"<strong>":  "%%STRONG_OPEN%%",
		"</strong>": "%%STRONG_CLOSE%%",
		"<br/>":     "%%BR%%",
		"</a>":      "%%A_CLOSE%%",
	}

	for tag, placeholder := range wmlTags {
		s = strings.ReplaceAll(s, tag, placeholder)
	}

	// Protect img tags with src and alt attributes
	reImg := regexp.MustCompile(`<img src="([^"]*)" alt="([^"]*)"/>`)
	imgMatches := reImg.FindAllStringSubmatch(s, -1)
	for i, match := range imgMatches {
		s = strings.Replace(s, match[0], fmt.Sprintf("%%IMG_%d%%", i), 1)
	}

	// Protect anchor tags with href attributes
	reAnchor := regexp.MustCompile(`<a href="/article\?id=(\d+)">`)
	anchorMatches := reAnchor.FindAllStringSubmatch(s, -1)
	for i, match := range anchorMatches {
		s = strings.Replace(s, match[0], fmt.Sprintf("%%ANCHOR_%d%%", i), 1)
	}

	// Escape special characters
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	s = strings.ReplaceAll(s, "$", "$$")

	// Restore WML tags
	for tag, placeholder := range wmlTags {
		s = strings.ReplaceAll(s, placeholder, tag)
	}

	// Restore img tags
	for i, match := range imgMatches {
		s = strings.Replace(s, fmt.Sprintf("%%IMG_%d%%", i), match[0], 1)
	}

	// Restore anchor tags
	for i, match := range anchorMatches {
		s = strings.Replace(s, fmt.Sprintf("%%ANCHOR_%d%%", i), match[0], 1)
	}

	return s
}

// escapeWML escapes special characters for WML
func escapeWML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	s = strings.ReplaceAll(s, "$", "$$")
	return s
}

// SplitContent splits content into chunks for pagination
func SplitContent(content string, maxLength int) []string {
	if len(content) <= maxLength {
		return []string{content}
	}

	var chunks []string
	lines := strings.Split(content, "\n")
	var current strings.Builder

	for _, line := range lines {
		if current.Len()+len(line)+1 > maxLength && current.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}

		if len(line) > maxLength {
			// Split long lines
			words := strings.Fields(line)
			for _, word := range words {
				if current.Len()+len(word)+1 > maxLength && current.Len() > 0 {
					chunks = append(chunks, strings.TrimSpace(current.String()))
					current.Reset()
				}
				if current.Len() > 0 {
					current.WriteByte(' ')
				}
				current.WriteString(word)
			}
		} else {
			if current.Len() > 0 {
				current.WriteByte('\n')
			}
			current.WriteString(line)
		}
	}

	if current.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}

	return chunks
}

// stripLeadingTitle removes the article title from the beginning of content
// since it's already displayed in the card title
func stripLeadingTitle(content string, title string) string {
	// Remove leading breaks
	content = strings.TrimPrefix(content, "<br/>")
	content = strings.TrimPrefix(content, "<br/>")

	// Pattern: title as plain text at start
	if after, ok := strings.CutPrefix(content, title); ok {
		content = after
	}

	// Pattern: title in bold at start
	boldTitle := "<b>" + title + "</b>"
	if after, ok := strings.CutPrefix(content, boldTitle); ok {
		content = after
	}

	// Pattern: breaks then title
	content = strings.TrimPrefix(content, "<br/>")
	content = strings.TrimPrefix(content, "<br/>")
	if after, ok := strings.CutPrefix(content, title); ok {
		content = after
	}
	if after, ok := strings.CutPrefix(content, boldTitle); ok {
		content = after
	}

	// Clean up leading breaks again
	for strings.HasPrefix(content, "<br/>") {
		content = strings.TrimPrefix(content, "<br/>")
	}

	return content
}

// FormatTitle formats a title for display in WML
func FormatTitle(title string) string {
	// Truncate long titles
	if len(title) > 30 {
		title = title[:27] + "..."
	}
	return escapeWML(title)
}

// removeNestedFormattingTags removes nested WML formatting tags
// WML does not support nested formatting like <i><i>text</i></i>
func removeNestedFormattingTags(content string) string {
	// Tags to check for nesting
	tags := []string{"i", "b", "u", "big", "small"}

	for _, tag := range tags {
		openTag := "<" + tag + ">"
		closeTag := "</" + tag + ">"

		// Remove nested opening tags: <i>...<i>... -> <i>...
		// Pattern: find opening tag, then another opening tag before closing
		for {
			result := removeOneNestedOpen(content, openTag, closeTag)
			if result == content {
				break
			}
			content = result
		}

		// Remove orphaned closing tags (more closing than opening)
		for {
			result := removeOrphanedClose(content, openTag, closeTag)
			if result == content {
				break
			}
			content = result
		}
	}

	return content
}

// removeOneNestedOpen removes one nested opening tag
func removeOneNestedOpen(content, openTag, closeTag string) string {
	depth := 0
	i := 0
	for i < len(content) {
		if strings.HasPrefix(content[i:], openTag) {
			if depth > 0 {
				// Found nested open tag, remove it
				return content[:i] + content[i+len(openTag):]
			}
			depth++
			i += len(openTag)
		} else if strings.HasPrefix(content[i:], closeTag) {
			depth--
			if depth < 0 {
				depth = 0
			}
			i += len(closeTag)
		} else {
			i++
		}
	}
	return content
}

// removeOrphanedClose removes orphaned closing tags
func removeOrphanedClose(content, openTag, closeTag string) string {
	depth := 0
	i := 0
	for i < len(content) {
		if strings.HasPrefix(content[i:], openTag) {
			depth++
			i += len(openTag)
		} else if strings.HasPrefix(content[i:], closeTag) {
			if depth <= 0 {
				// Found orphaned closing tag, remove it
				return content[:i] + content[i+len(closeTag):]
			}
			depth--
			i += len(closeTag)
		} else {
			i++
		}
	}
	return content
}
