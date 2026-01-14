package wikipedia

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/blugelabs/bluge"
)

// BlugeIndex handles the persistent search index
type BlugeIndex struct {
	reader    *bluge.Reader
	path      string
	docCount  uint64 // Cached document count
	docCached bool   // Whether doc count has been cached
	cacheMu   sync.RWMutex
}

// DefaultIndexPath returns the default index path for a ZIM file
func DefaultIndexPath(zimPath string) string {
	// Use .bluge extension next to the ZIM file
	return strings.TrimSuffix(zimPath, filepath.Ext(zimPath)) + ".bluge"
}

// indexEntry represents an entry to be indexed
type indexEntry struct {
	idx   uint32
	title string
	url   string
}

// BuildBlugeIndex creates a new Bluge index from a ZIM file using multiple workers
func BuildBlugeIndex(zimPath, indexPath string) error {
	// Open ZIM file
	reader, err := NewZIMReader(zimPath)
	if err != nil {
		return fmt.Errorf("failed to open ZIM file: %w", err)
	}
	defer reader.Close()

	// Remove existing index if it exists
	if _, err := os.Stat(indexPath); err == nil {
		fmt.Printf("Removing existing index at %s\n", indexPath)
		if err := os.RemoveAll(indexPath); err != nil {
			return fmt.Errorf("failed to remove existing index: %w", err)
		}
	}

	// Create index config
	config := bluge.DefaultConfig(indexPath)
	writer, err := bluge.OpenWriter(config)
	if err != nil {
		return fmt.Errorf("failed to create index: %w", err)
	}
	defer writer.Close()

	entryCount := reader.GetArticleCount()
	numWorkers := runtime.NumCPU()
	batchSize := 10000
	channelBuffer := numWorkers * 1000

	fmt.Printf("Building Bluge index from %s\n", zimPath)
	fmt.Printf("Total entries to process: %d (using %d workers)\n", entryCount, numWorkers)

	// Channels for pipeline
	entryChan := make(chan indexEntry, channelBuffer)
	docChan := make(chan *bluge.Document, channelBuffer)
	errChan := make(chan error, 1)

	// Progress tracking
	var processedCount atomic.Uint64
	var articleCount atomic.Uint64
	logInterval := uint64(entryCount) / 20
	if logInterval == 0 {
		logInterval = 1
	}

	// WaitGroups for coordination
	var readerWg sync.WaitGroup
	var workerWg sync.WaitGroup
	var writerWg sync.WaitGroup

	// Start reader goroutine - reads directory entries from ZIM
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		defer close(entryChan)

		for i := uint32(0); i < entryCount; i++ {
			entry, err := reader.GetDirectoryEntry(i)
			if err != nil {
				continue
			}

			// Only index articles (namespace 'A' or 'C')
			if entry.Namespace != 'A' && entry.Namespace != 'C' {
				continue
			}

			// Skip redirects
			if entry.IsRedirect {
				continue
			}

			// Skip resource files
			url := strings.ToLower(entry.URL)
			if strings.HasSuffix(url, ".css") || strings.HasSuffix(url, ".js") ||
				strings.HasSuffix(url, ".png") || strings.HasSuffix(url, ".jpg") ||
				strings.HasSuffix(url, ".jpeg") || strings.HasSuffix(url, ".gif") ||
				strings.HasSuffix(url, ".svg") || strings.HasSuffix(url, ".ico") ||
				strings.HasSuffix(url, ".woff") || strings.HasSuffix(url, ".woff2") ||
				strings.HasSuffix(url, ".ttf") || strings.HasSuffix(url, ".eot") ||
				strings.Contains(url, "/-/") {
				continue
			}

			entryChan <- indexEntry{
				idx:   i,
				title: entry.Title,
				url:   entry.URL,
			}
		}
	}()

	// Start worker goroutines - create Bluge documents
	for w := 0; w < numWorkers; w++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()

			for entry := range entryChan {
				// Create document
				doc := bluge.NewDocument(strconv.FormatUint(uint64(entry.idx), 10))

				// Add title field (searchable and stored)
				doc.AddField(bluge.NewTextField("title", entry.title).StoreValue().SearchTermPositions())

				// Add title_lower for case-insensitive exact matching
				doc.AddField(bluge.NewKeywordField("title_exact", strings.ToLower(entry.title)).StoreValue())

				// Add URL field (stored only)
				doc.AddField(bluge.NewKeywordField("url", entry.url).StoreValue())

				// Add index as numeric field for retrieval
				doc.AddField(bluge.NewNumericField("idx", float64(entry.idx)).StoreValue())

				docChan <- doc
			}
		}()
	}

	// Close docChan when all workers are done
	go func() {
		workerWg.Wait()
		close(docChan)
	}()

	// Start writer goroutine - batches and writes documents to index
	writerWg.Add(1)
	go func() {
		defer writerWg.Done()

		batch := bluge.NewBatch()
		batchCount := 0

		for doc := range docChan {
			batch.Insert(doc)
			batchCount++
			count := articleCount.Add(1)
			processed := processedCount.Add(1)

			// Log progress
			if processed%logInterval == 0 {
				pct := (processed * 100) / uint64(entryCount)
				fmt.Printf("Building index: %d%% complete (%d articles indexed)\n", pct, count)
			}

			// Flush batch periodically
			if batchCount >= batchSize {
				if err := writer.Batch(batch); err != nil {
					select {
					case errChan <- fmt.Errorf("failed to write batch: %w", err):
					default:
					}
					return
				}
				batch = bluge.NewBatch()
				batchCount = 0
			}
		}

		// Flush remaining documents
		if batchCount > 0 {
			if err := writer.Batch(batch); err != nil {
				select {
				case errChan <- fmt.Errorf("failed to write final batch: %w", err):
				default:
				}
				return
			}
		}
	}()

	// Wait for reader to finish
	readerWg.Wait()

	// Wait for writer to finish
	writerWg.Wait()

	// Check for errors
	select {
	case err := <-errChan:
		return err
	default:
	}

	finalCount := articleCount.Load()
	fmt.Printf("Index complete: %d articles indexed to %s\n", finalCount, indexPath)
	return nil
}

// LoadBlugeIndex loads an existing Bluge index
func LoadBlugeIndex(indexPath string) (*BlugeIndex, error) {
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("index not found at %s", indexPath)
	}

	config := bluge.DefaultConfig(indexPath)
	reader, err := bluge.OpenReader(config)
	if err != nil {
		return nil, fmt.Errorf("failed to open index: %w", err)
	}

	return &BlugeIndex{
		reader: reader,
		path:   indexPath,
	}, nil
}

// Close closes the Bluge index reader
func (b *BlugeIndex) Close() error {
	if b.reader != nil {
		return b.reader.Close()
	}
	return nil
}

// Search performs a search query and returns results
func (b *BlugeIndex) Search(query string, maxResults int) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	log.Printf("Bluge search: query=%q, maxResults=%d", query, maxResults)
	ctx := context.Background()

	// Build a query that matches title field
	// Use a boolean query with should clauses for flexible matching
	queryLower := strings.ToLower(query)

	// Pre-allocate queries slice to avoid reallocations
	queryCapacity := 5
	if len(query) <= 3 {
		queryCapacity = 4 // No fuzzy query for short queries
	}
	queries := make([]bluge.Query, 0, queryCapacity)

	// 1. Exact title match (highest priority)
	exactQuery := bluge.NewTermQuery(queryLower).SetField("title_exact").SetBoost(100.0)
	queries = append(queries, exactQuery)

	// 2. Prefix match on exact title
	prefixQuery := bluge.NewPrefixQuery(queryLower).SetField("title_exact").SetBoost(50.0)
	queries = append(queries, prefixQuery)

	// 3. Match query on title (full-text search with analysis)
	matchQuery := bluge.NewMatchQuery(query).SetField("title").SetBoost(10.0)
	queries = append(queries, matchQuery)

	// 4. Fuzzy match for typo tolerance (skip for short queries - expensive)
	if len(query) > 3 {
		fuzzyQuery := bluge.NewFuzzyQuery(queryLower).SetField("title_exact").SetFuzziness(1).SetBoost(5.0)
		queries = append(queries, fuzzyQuery)
	}

	// 5. Wildcard for partial matches
	wildcardQuery := bluge.NewWildcardQuery("*" + queryLower + "*").SetField("title_exact").SetBoost(3.0)
	queries = append(queries, wildcardQuery)

	// Combine with boolean OR
	boolQuery := bluge.NewBooleanQuery()
	for _, q := range queries {
		boolQuery.AddShould(q)
	}
	boolQuery.SetMinShould(1)

	// Execute search
	searchReq := bluge.NewTopNSearch(maxResults, boolQuery).WithStandardAggregations()
	docMatches, err := b.reader.Search(ctx, searchReq)
	if err != nil {
		log.Printf("Bluge search error: %v", err)
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Pre-allocate results slice
	results := make([]SearchResult, 0, maxResults)

	// Iterate through results
	match, err := docMatches.Next()
	for err == nil && match != nil {
		var result SearchResult
		result.Score = match.Score

		// Load stored fields
		err = match.VisitStoredFields(func(field string, value []byte) bool {
			switch field {
			case "title":
				result.Title = string(value)
			case "url":
				result.URL = string(value)
			case "idx":
				// Decode numeric field
				if num, err := bluge.DecodeNumericFloat64(value); err == nil {
					result.Index = uint32(num)
				}
			case "_id":
				// Document ID is the index as string
				if idx, err := strconv.ParseUint(string(value), 10, 32); err == nil {
					result.Index = uint32(idx)
				}
			}
			return true
		})
		if err != nil {
			break
		}

		results = append(results, result)
		match, err = docMatches.Next()
	}

	if err != nil {
		log.Printf("Bluge search iteration error: %v", err)
		return nil, fmt.Errorf("error iterating results: %w", err)
	}

	log.Printf("Bluge search complete: %d results for %q", len(results), query)
	return results, nil
}

// GetDocumentCount returns the number of documents in the index (cached after first call)
func (b *BlugeIndex) GetDocumentCount() (uint64, error) {
	// Check cache first
	b.cacheMu.RLock()
	if b.docCached {
		count := b.docCount
		b.cacheMu.RUnlock()
		return count, nil
	}
	b.cacheMu.RUnlock()

	// Need to compute
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()

	// Double-check after acquiring write lock
	if b.docCached {
		return b.docCount, nil
	}

	log.Println("Computing document count for search index...")
	ctx := context.Background()

	// Use a match all query with count aggregation
	query := bluge.NewMatchAllQuery()
	searchReq := bluge.NewTopNSearch(0, query).WithStandardAggregations()

	docMatches, err := b.reader.Search(ctx, searchReq)
	if err != nil {
		return 0, err
	}

	count := docMatches.Aggregations().Count()
	b.docCount = count
	b.docCached = true
	log.Printf("Document count: %d (cached)", count)
	return count, nil
}

// GetRandomArticleIndex returns a random article index from the search index
func (b *BlugeIndex) GetRandomArticleIndex() (uint32, error) {
	ctx := context.Background()

	// Get total document count (cached if available)
	count, err := b.GetDocumentCount()
	if err != nil {
		return 0, fmt.Errorf("failed to get document count: %w", err)
	}
	if count == 0 {
		return 0, fmt.Errorf("no articles in index")
	}

	// Pick a random offset using crypto/rand (no seeding required)
	var buf [8]byte
	if _, err := cryptorand.Read(buf[:]); err != nil {
		return 0, fmt.Errorf("failed to generate random number: %w", err)
	}
	offset := int(binary.LittleEndian.Uint64(buf[:]) % count)

	// Use match all query with a small result set - skip to offset efficiently
	// Only fetch the single document we need
	query := bluge.NewMatchAllQuery()

	// Limit search to just what we need (offset + 1)
	searchReq := bluge.NewTopNSearch(offset+1, query)

	docMatches, err := b.reader.Search(ctx, searchReq)
	if err != nil {
		return 0, fmt.Errorf("search failed: %w", err)
	}

	// Skip to the random offset - iterate until we reach target
	var docMatch, matchErr = docMatches.Next()
	for i := 0; i < offset && matchErr == nil && docMatch != nil; i++ {
		docMatch, matchErr = docMatches.Next()
	}
	if matchErr != nil {
		return 0, fmt.Errorf("error iterating to offset: %w", matchErr)
	}
	if docMatch == nil {
		return 0, fmt.Errorf("unexpected end of results at offset %d", offset)
	}

	// Extract the article index from the final match
	var articleIdx uint32
	var found bool
	err = docMatch.VisitStoredFields(func(field string, value []byte) bool {
		if field == "idx" {
			if num, decErr := bluge.DecodeNumericFloat64(value); decErr == nil {
				articleIdx = uint32(num)
				found = true
				return false // Stop visiting fields once we found idx
			}
		} else if field == "_id" && !found {
			// Fallback: document ID is the index as string
			if idx, parseErr := strconv.ParseUint(string(value), 10, 32); parseErr == nil {
				articleIdx = uint32(idx)
				found = true
				return false
			}
		}
		return true
	})
	if err != nil {
		return 0, fmt.Errorf("error reading stored fields: %w", err)
	}
	if !found {
		return 0, fmt.Errorf("article index not found in document")
	}

	return articleIdx, nil
}
