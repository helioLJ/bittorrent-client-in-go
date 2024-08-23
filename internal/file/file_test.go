package file

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func TestFileManager(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "filemanager_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	fm := NewFileManager(tempDir)

	// Test CreateFiles
	err = fm.CreateFiles("test", []int64{100, 200})
	if err != nil {
		t.Fatalf("CreateFiles failed: %v", err)
	}

	// Check if files were created
	files, err := ioutil.ReadDir(filepath.Join(tempDir, "test"))
	if err != nil {
		t.Fatalf("Failed to read dir: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("Expected 2 files, got %d", len(files))
	}

	// Test WritePiece
	data := []byte("Hello, World!")
	err = fm.WritePiece(0, 0, data)
	if err != nil {
		t.Fatalf("WritePiece failed: %v", err)
	}

	// Verify written data
	content, err := ioutil.ReadFile(filepath.Join(tempDir, "test", "part1"))
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(content[:len(data)]) != string(data) {
		t.Errorf("Expected %s, got %s", string(data), string(content[:len(data)]))
	}

	// Test Close
	err = fm.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}