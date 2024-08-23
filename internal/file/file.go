package file

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileManager handles file operations for torrents
type FileManager struct {
	baseDir string
	files   []*os.File
	mutex   sync.Mutex
}

// NewFileManager creates a new FileManager
func NewFileManager(baseDir string) *FileManager {
	return &FileManager{
		baseDir: baseDir,
		files:   make([]*os.File, 0),
	}
}

// CreateFiles creates the necessary files for the torrent
func (fm *FileManager) CreateFiles(name string, lengths []int64) error {
	fm.mutex.Lock()
	defer fm.mutex.Unlock()

	for i, length := range lengths {
		path := filepath.Join(fm.baseDir, name)
		if len(lengths) > 1 {
			path = filepath.Join(path, fmt.Sprintf("part%d", i+1))
		}

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}

		file, err := os.Create(path)
		if err != nil {
			return err
		}

		if err := file.Truncate(length); err != nil {
			file.Close()
			return err
		}

		fm.files = append(fm.files, file)
	}

	return nil
}

// WritePiece writes a piece of the file to disk
func (fm *FileManager) WritePiece(index int, offset int64, data []byte) error {
	fm.mutex.Lock()
	defer fm.mutex.Unlock()

	if index < 0 || index >= len(fm.files) {
		return errors.New("invalid file index")
	}

	_, err := fm.files[index].WriteAt(data, offset)
	return err
}

// Close closes all open files
func (fm *FileManager) Close() error {
	fm.mutex.Lock()
	defer fm.mutex.Unlock()

	for _, file := range fm.files {
		if err := file.Close(); err != nil {
			return err
		}
	}

	fm.files = nil
	return nil
}