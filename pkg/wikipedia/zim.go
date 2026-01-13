package wikipedia

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// ZIM file format constants
const (
	ZimMagicNumber = 0x44D495A // ZIM magic number (little endian)
)

// ZIMHeader represents the header of a ZIM file
type ZIMHeader struct {
	MagicNumber   uint32
	MajorVersion  uint16
	MinorVersion  uint16
	UUID          [16]byte
	ArticleCount  uint32
	ClusterCount  uint32
	URLPtrPos     uint64
	TitlePtrPos   uint64
	ClusterPtrPos uint64
	MimeListPos   uint64
	MainPage      uint32
	LayoutPage    uint32
	ChecksumPos   uint64
}

// DirectoryEntry represents an entry in the ZIM directory
type DirectoryEntry struct {
	MimeType    uint16
	ParamLen    uint8
	Namespace   byte
	Revision    uint32
	ClusterNum  uint32
	BlobNum     uint32
	URL         string
	Title       string
	RedirectIdx uint32
	IsRedirect  bool
}

// ZIMReader handles reading ZIM files
type ZIMReader struct {
	file        *os.File
	header      ZIMHeader
	mimeTypes   []string
	urlPtrs     []uint64
	titlePtrs   []uint32
	clusterPtrs []uint64
	mu          sync.RWMutex
}

// NewZIMReader creates a new ZIM file reader
func NewZIMReader(filepath string) (*ZIMReader, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to open ZIM file: %w", err)
	}

	reader := &ZIMReader{file: file}
	if err := reader.readHeader(); err != nil {
		file.Close()
		return nil, err
	}

	if err := reader.readMimeTypes(); err != nil {
		file.Close()
		return nil, err
	}

	if err := reader.readURLPointers(); err != nil {
		file.Close()
		return nil, err
	}

	if err := reader.readClusterPointers(); err != nil {
		file.Close()
		return nil, err
	}

	return reader, nil
}

// Close closes the ZIM file
func (z *ZIMReader) Close() error {
	return z.file.Close()
}

func (z *ZIMReader) readHeader() error {
	z.mu.Lock()
	defer z.mu.Unlock()

	if _, err := z.file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	if err := binary.Read(z.file, binary.LittleEndian, &z.header); err != nil {
		return fmt.Errorf("failed to read ZIM header: %w", err)
	}

	if z.header.MagicNumber != ZimMagicNumber {
		return errors.New("invalid ZIM file: magic number mismatch")
	}

	return nil
}

func (z *ZIMReader) readMimeTypes() error {
	z.mu.Lock()
	defer z.mu.Unlock()

	if _, err := z.file.Seek(int64(z.header.MimeListPos), io.SeekStart); err != nil {
		return err
	}

	z.mimeTypes = []string{}
	for {
		var buf bytes.Buffer
		b := make([]byte, 1)
		for {
			if _, err := z.file.Read(b); err != nil {
				return err
			}
			if b[0] == 0 {
				break
			}
			buf.WriteByte(b[0])
		}
		if buf.Len() == 0 {
			break
		}
		z.mimeTypes = append(z.mimeTypes, buf.String())
	}

	return nil
}

func (z *ZIMReader) readURLPointers() error {
	z.mu.Lock()
	defer z.mu.Unlock()

	if _, err := z.file.Seek(int64(z.header.URLPtrPos), io.SeekStart); err != nil {
		return err
	}

	z.urlPtrs = make([]uint64, z.header.ArticleCount)
	for i := uint32(0); i < z.header.ArticleCount; i++ {
		if err := binary.Read(z.file, binary.LittleEndian, &z.urlPtrs[i]); err != nil {
			return err
		}
	}

	return nil
}

func (z *ZIMReader) readClusterPointers() error {
	z.mu.Lock()
	defer z.mu.Unlock()

	if _, err := z.file.Seek(int64(z.header.ClusterPtrPos), io.SeekStart); err != nil {
		return err
	}

	z.clusterPtrs = make([]uint64, z.header.ClusterCount)
	for i := uint32(0); i < z.header.ClusterCount; i++ {
		if err := binary.Read(z.file, binary.LittleEndian, &z.clusterPtrs[i]); err != nil {
			return err
		}
	}

	return nil
}

// GetDirectoryEntry reads a directory entry at the given index
func (z *ZIMReader) GetDirectoryEntry(idx uint32) (*DirectoryEntry, error) {
	if idx >= z.header.ArticleCount {
		return nil, errors.New("index out of range")
	}

	z.mu.RLock()
	ptr := z.urlPtrs[idx]
	z.mu.RUnlock()

	z.mu.Lock()
	defer z.mu.Unlock()

	if _, err := z.file.Seek(int64(ptr), io.SeekStart); err != nil {
		return nil, err
	}

	entry := &DirectoryEntry{}

	if err := binary.Read(z.file, binary.LittleEndian, &entry.MimeType); err != nil {
		return nil, err
	}

	if err := binary.Read(z.file, binary.LittleEndian, &entry.ParamLen); err != nil {
		return nil, err
	}

	if err := binary.Read(z.file, binary.LittleEndian, &entry.Namespace); err != nil {
		return nil, err
	}

	if err := binary.Read(z.file, binary.LittleEndian, &entry.Revision); err != nil {
		return nil, err
	}

	// Check if it's a redirect (mime type = 0xFFFF)
	if entry.MimeType == 0xFFFF {
		entry.IsRedirect = true
		if err := binary.Read(z.file, binary.LittleEndian, &entry.RedirectIdx); err != nil {
			return nil, err
		}
	} else {
		if err := binary.Read(z.file, binary.LittleEndian, &entry.ClusterNum); err != nil {
			return nil, err
		}
		if err := binary.Read(z.file, binary.LittleEndian, &entry.BlobNum); err != nil {
			return nil, err
		}
	}

	// Read URL (null-terminated)
	var urlBuf bytes.Buffer
	b := make([]byte, 1)
	for {
		if _, err := z.file.Read(b); err != nil {
			return nil, err
		}
		if b[0] == 0 {
			break
		}
		urlBuf.WriteByte(b[0])
	}
	entry.URL = urlBuf.String()

	// Read Title (null-terminated)
	var titleBuf bytes.Buffer
	for {
		if _, err := z.file.Read(b); err != nil {
			return nil, err
		}
		if b[0] == 0 {
			break
		}
		titleBuf.WriteByte(b[0])
	}
	entry.Title = titleBuf.String()
	if entry.Title == "" {
		entry.Title = entry.URL
	}

	return entry, nil
}

// GetArticleCount returns the number of articles in the ZIM file
func (z *ZIMReader) GetArticleCount() uint32 {
	return z.header.ArticleCount
}

// GetMainPageIndex returns the index of the main page
func (z *ZIMReader) GetMainPageIndex() uint32 {
	return z.header.MainPage
}

// GetBlob reads a blob from a cluster
func (z *ZIMReader) GetBlob(clusterNum, blobNum uint32) ([]byte, error) {
	if clusterNum >= z.header.ClusterCount {
		return nil, errors.New("cluster index out of range")
	}

	z.mu.RLock()
	clusterPtr := z.clusterPtrs[clusterNum]
	var nextClusterPtr uint64
	if clusterNum+1 < z.header.ClusterCount {
		nextClusterPtr = z.clusterPtrs[clusterNum+1]
	} else {
		nextClusterPtr = z.header.ChecksumPos
	}
	z.mu.RUnlock()

	z.mu.Lock()
	defer z.mu.Unlock()

	if _, err := z.file.Seek(int64(clusterPtr), io.SeekStart); err != nil {
		return nil, err
	}

	// Read cluster info byte
	clusterInfo := make([]byte, 1)
	if _, err := z.file.Read(clusterInfo); err != nil {
		return nil, err
	}

	compression := clusterInfo[0] & 0x0F

	clusterSize := int64(nextClusterPtr - clusterPtr - 1)
	compressedData := make([]byte, clusterSize)
	if _, err := io.ReadFull(z.file, compressedData); err != nil {
		return nil, err
	}

	var clusterData []byte
	var err error

	switch compression {
	case 0, 1: // uncompressed
		clusterData = compressedData
	case 4: // zlib/deflate
		reader := flate.NewReader(bytes.NewReader(compressedData))
		clusterData, err = io.ReadAll(reader)
		reader.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to decompress zlib cluster: %w", err)
		}
	case 5: // LZMA/XZ - try XZ first, then fall back to zstd
		reader, err := xz.NewReader(bytes.NewReader(compressedData))
		if err != nil {
			// Try zstd as fallback (some ZIM files mislabel compression)
			decoder, zstdErr := zstd.NewReader(bytes.NewReader(compressedData))
			if zstdErr != nil {
				return nil, fmt.Errorf("failed to create XZ reader: %w (zstd fallback also failed: %v)", err, zstdErr)
			}
			clusterData, err = io.ReadAll(decoder)
			decoder.Close()
			if err != nil {
				return nil, fmt.Errorf("failed to decompress with zstd fallback: %w", err)
			}
		} else {
			clusterData, err = io.ReadAll(reader)
			if err != nil {
				return nil, fmt.Errorf("failed to decompress XZ cluster: %w", err)
			}
		}
	case 6: // zstd
		decoder, err := zstd.NewReader(bytes.NewReader(compressedData))
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd reader: %w", err)
		}
		defer decoder.Close()
		clusterData, err = io.ReadAll(decoder)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress zstd cluster: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported compression type: %d", compression)
	}

	// Parse blob offsets (4 bytes each)
	offsetReader := bytes.NewReader(clusterData)

	// Read first offset to determine number of blobs
	var firstOffset uint32
	if err := binary.Read(offsetReader, binary.LittleEndian, &firstOffset); err != nil {
		return nil, err
	}

	numBlobs := firstOffset / 4
	if blobNum >= numBlobs {
		return nil, fmt.Errorf("blob index %d out of range (max %d)", blobNum, numBlobs-1)
	}

	offsets := make([]uint32, numBlobs+1)
	offsets[0] = firstOffset

	for i := uint32(1); i <= numBlobs; i++ {
		if i == numBlobs {
			offsets[i] = uint32(len(clusterData))
		} else {
			if err := binary.Read(offsetReader, binary.LittleEndian, &offsets[i]); err != nil {
				return nil, err
			}
		}
	}

	blobStart := offsets[blobNum]
	blobEnd := offsets[blobNum+1]

	if blobStart > uint32(len(clusterData)) || blobEnd > uint32(len(clusterData)) {
		return nil, errors.New("blob offset out of range")
	}

	return clusterData[blobStart:blobEnd], nil
}

// GetArticleContent retrieves the content of an article by its index
func (z *ZIMReader) GetArticleContent(idx uint32) ([]byte, string, error) {
	entry, err := z.GetDirectoryEntry(idx)
	if err != nil {
		return nil, "", err
	}

	// Follow redirects
	for entry.IsRedirect {
		entry, err = z.GetDirectoryEntry(entry.RedirectIdx)
		if err != nil {
			return nil, "", err
		}
	}

	content, err := z.GetBlob(entry.ClusterNum, entry.BlobNum)
	if err != nil {
		return nil, "", err
	}

	mimeType := ""
	if int(entry.MimeType) < len(z.mimeTypes) {
		mimeType = z.mimeTypes[entry.MimeType]
	}

	return content, mimeType, nil
}

// FindArticleByURL finds an article by its URL
func (z *ZIMReader) FindArticleByURL(namespace byte, url string) (uint32, error) {
	// Binary search through URL pointers
	left := uint32(0)
	right := z.header.ArticleCount - 1

	for left <= right {
		mid := (left + right) / 2
		entry, err := z.GetDirectoryEntry(mid)
		if err != nil {
			return 0, err
		}

		cmp := compareNamespaceURL(entry.Namespace, entry.URL, namespace, url)
		if cmp == 0 {
			return mid, nil
		} else if cmp < 0 {
			left = mid + 1
		} else {
			if mid == 0 {
				break
			}
			right = mid - 1
		}
	}

	return 0, errors.New("article not found")
}

func compareNamespaceURL(ns1 byte, url1 string, ns2 byte, url2 string) int {
	if ns1 < ns2 {
		return -1
	}
	if ns1 > ns2 {
		return 1
	}
	return strings.Compare(url1, url2)
}
