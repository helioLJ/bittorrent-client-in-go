package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
)

var (
	logger *log.Logger
	once   sync.Once
)

// InitLogger initializes the logger
func InitLogger(out io.Writer) {
	once.Do(func() {
		logger = log.New(out, "bittorrent-client: ", log.LstdFlags|log.Lshortfile)
	})
}

// Info logs an informational message
func Info(format string, v ...interface{}) {
	if logger == nil {
		InitLogger(os.Stdout)
	}
	logger.Output(2, fmt.Sprintf("INFO: "+format, v...))
}

// Error logs an error message
func Error(format string, v ...interface{}) {
	if logger == nil {
		InitLogger(os.Stderr)
	}
	logger.Output(2, fmt.Sprintf("ERROR: "+format, v...))
}

// Debug logs a debug message
func Debug(format string, v ...interface{}) {
	if logger == nil {
		InitLogger(os.Stdout)
	}
	logger.Output(2, fmt.Sprintf("DEBUG: "+format, v...))
}