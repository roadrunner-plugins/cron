package cron

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// rotatingWriter implements a size-based rotating file writer
type rotatingWriter struct {
	filename    string
	maxSize     int64
	maxFiles    int
	currentSize int64
	file        *os.File
	mu          sync.Mutex
}

// newRotatingWriter creates a new rotating file writer
func newRotatingWriter(filename string, maxSize int64, maxFiles int) (*rotatingWriter, error) {
	// Create directory if it doesn't exist
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Open or create the file
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	// Get current file size
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to stat log file: %w", err)
	}

	return &rotatingWriter{
		filename:    filename,
		maxSize:     maxSize,
		maxFiles:    maxFiles,
		currentSize: info.Size(),
		file:        file,
	}, nil
}

// Write writes data to the file and rotates if necessary
func (w *rotatingWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Check if rotation is needed
	if w.currentSize+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, fmt.Errorf("failed to rotate log file: %w", err)
		}
	}

	// Write to file
	n, err = w.file.Write(p)
	if err != nil {
		return n, err
	}

	w.currentSize += int64(n)
	return n, nil
}

// rotate rotates the log file
func (w *rotatingWriter) rotate() error {
	// Close current file
	if err := w.file.Close(); err != nil {
		return err
	}

	// Rotate existing files
	for i := w.maxFiles - 1; i > 0; i-- {
		oldPath := fmt.Sprintf("%s.%d", w.filename, i)
		newPath := fmt.Sprintf("%s.%d", w.filename, i+1)

		// Remove oldest file if it exists
		if i == w.maxFiles-1 {
			_ = os.Remove(newPath)
		}

		// Rename file if it exists
		if _, err := os.Stat(oldPath); err == nil {
			if err := os.Rename(oldPath, newPath); err != nil {
				return err
			}
		}
	}

	// Rename current file to .1
	if err := os.Rename(w.filename, fmt.Sprintf("%s.1", w.filename)); err != nil {
		return err
	}

	// Create new file
	file, err := os.OpenFile(w.filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	w.file = file
	w.currentSize = 0
	return nil
}

// Close closes the file
func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

// Ensure rotatingWriter implements io.WriteCloser
var _ io.WriteCloser = (*rotatingWriter)(nil)
