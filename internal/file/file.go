package file

import (
	"bittorrent-client/internal/common"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type FileInfo struct {
    File   *os.File
    Length int64
}

type FileManager struct {
    baseDir      string
    files        []*FileInfo
    mutex        sync.Mutex
    pieceSize    int64
    pieceOffsets []int64
    totalLength  int64
    numPieces    int // Added this field
}

// NewFileManager creates a new FileManager
func NewFileManager(baseDir string) *FileManager {
    return &FileManager{
        baseDir: baseDir,
        files:   make([]*FileInfo, 0),
    }
}

// SetPieceSize sets the piece size for the FileManager
func (fm *FileManager) SetPieceSize(size int64) {
    fm.pieceSize = size
    fm.numPieces = int((fm.totalLength + fm.pieceSize - 1) / fm.pieceSize)
}

func (fm *FileManager) SetTotalLength(length int64) {
    fm.totalLength = length
    fm.numPieces = int((fm.totalLength + fm.pieceSize - 1) / fm.pieceSize)
}

// Initialize function should be updated to use torrent.TorrentFile
func (fm *FileManager) Initialize(files []common.TorrentFile) error {
    fm.files = make([]*FileInfo, len(files))
    var offset int64 = 0

    for i, file := range files {
        filePath := filepath.Join(fm.baseDir, file.Path)
        if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
            return err
        }

        f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0644)
        if err != nil {
            return err
        }

        fm.files[i] = &FileInfo{
            File:   f,
            Length: file.Length,
        }

        fm.pieceOffsets = append(fm.pieceOffsets, offset)
        offset += file.Length
    }

    fm.totalLength = offset
    fm.pieceOffsets = append(fm.pieceOffsets, offset) // Add final offset
    
    // Calculate the number of pieces
    if fm.pieceSize > 0 {
        fm.numPieces = int((fm.totalLength + fm.pieceSize - 1) / fm.pieceSize)
    } else {
        fm.numPieces = 1 // Default to 1 if pieceSize is not set
    }

    return nil
}

// WritePiece writes a piece of the file to disk
func (fm *FileManager) WritePiece(index int, offset int64, data []byte) error {
    fm.mutex.Lock()
    defer fm.mutex.Unlock()

    pieceOffset := int64(index) * fm.pieceSize + offset
    remainingData := data

    for i, fileInfo := range fm.files {
        if pieceOffset >= fileInfo.Length {
            pieceOffset -= fileInfo.Length
            continue
        }

        writeLength := min(int64(len(remainingData)), fileInfo.Length-pieceOffset)
        if _, err := fileInfo.File.WriteAt(remainingData[:writeLength], pieceOffset); err != nil {
            return fmt.Errorf("failed to write to file %d: %w", i, err)
        }

        remainingData = remainingData[writeLength:]
        if len(remainingData) == 0 {
            break
        }

        pieceOffset = 0
    }

    return nil
}

// Helper function to find the minimum of two int64 values
func min(a, b int64) int64 {
    if a < b {
        return a
    }
    return b
}

// Close function should be updated to close FileInfo.File
func (fm *FileManager) Close() error {
    fm.mutex.Lock()
    defer fm.mutex.Unlock()

    for _, fileInfo := range fm.files {
        if err := fileInfo.File.Close(); err != nil {
            return err
        }
    }

    fm.files = nil
    return nil
}

// ReadPiece reads a piece or part of a piece from the file(s)
func (fm *FileManager) ReadPiece(index int, begin int64, length int64) ([]byte, error) {
    fm.mutex.Lock()
    defer fm.mutex.Unlock()

    data := make([]byte, length)
    offset := int64(index)*fm.pieceSize + begin
    bytesRead := int64(0)

    for _, file := range fm.files {
        fileInfo, err := file.File.Stat()
        if err != nil {
            return nil, err
        }

        fileSize := fileInfo.Size()
        if offset >= fileSize {
            offset -= fileSize
            continue
        }

        _, err = file.File.Seek(offset, 0)
        if err != nil {
            return nil, err
        }

        n, err := file.File.Read(data[bytesRead:])
        if err != nil && err != io.EOF {
            return nil, err
        }

        bytesRead += int64(n)
        if bytesRead == length {
            break
        }

        offset = 0
    }

    return data[:bytesRead], nil
}

// Flush ensures all data is written to disk
func (fm *FileManager) Flush() error {
    fm.mutex.Lock()
    defer fm.mutex.Unlock()

    for _, file := range fm.files {
        if err := file.File.Sync(); err != nil {
            return fmt.Errorf("failed to flush file: %w", err)
        }
    }
    return nil
}

// CreateFiles creates the necessary files for the torrent
func (fm *FileManager) CreateFiles(name string, lengths []int64) error {
    for i, length := range lengths {
        filePath := filepath.Join(fm.baseDir, name)
        if i > 0 {
            // For multi-file torrents, append an index to the filename
            filePath = filepath.Join(filePath, fmt.Sprintf("file_%d", i))
        }
        
        // Ensure the directory exists
        dir := filepath.Dir(filePath)
        if err := os.MkdirAll(dir, 0755); err != nil {
            return fmt.Errorf("failed to create directory %s: %w", dir, err)
        }
        
        // Create the file
        f, err := os.Create(filePath)
        if err != nil {
            return fmt.Errorf("failed to create file %s: %w", filePath, err)
        }
        
        // Set the file size
        if err := f.Truncate(length); err != nil {
            f.Close()
            return fmt.Errorf("failed to set size for file %s: %w", filePath, err)
        }
        
        f.Close()
    }
    
    return nil
}