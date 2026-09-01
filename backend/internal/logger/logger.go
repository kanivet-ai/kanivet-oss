// Package logger provides build-time controlled logging.
// In production builds (built with -tags production), all logging is disabled
// and compiled out of the binary. This prevents customers from enabling logs.
package logger

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

var (
	mu          sync.RWMutex
	initialized bool
	logFile     *os.File
)

// Init initializes the logger.
// In production builds, this is a no-op.
func Init() error {
	if IsProduction() {
		// In production, completely silence all log output
		log.SetOutput(io.Discard)
		log.SetFlags(0)
		return nil
	}

	mu.Lock()
	defer mu.Unlock()

	if initialized {
		return nil
	}

	// Development: set up file logging
	execPath, err := os.Executable()
	if err != nil {
		log.SetOutput(os.Stdout)
		log.SetFlags(log.LstdFlags | log.Lshortfile)
		initialized = true
		return nil
	}

	projectRoot := filepath.Dir(filepath.Dir(filepath.Dir(execPath)))
	logDir := filepath.Join(projectRoot, "backend", "logs")

	if err := os.MkdirAll(logDir, 0755); err == nil {
		logPath := filepath.Join(logDir, "kanivet-backend.log")
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			logFile = f
			multiWriter := io.MultiWriter(os.Stdout, logFile)
			log.SetOutput(multiWriter)
		}
	}

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("[Logger] Initialized in development mode")
	initialized = true
	return nil
}

// Close cleans up resources
func Close() {
	if IsProduction() {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
}

// Debug logs a debug message (compiled out in production)
func Debug(format string, v ...interface{}) {
	if IsProduction() {
		return
	}
	log.Printf("[DEBUG] "+format, v...)
}

// Info logs an info message (compiled out in production)
func Info(format string, v ...interface{}) {
	if IsProduction() {
		return
	}
	log.Printf("[INFO] "+format, v...)
}

// Warn logs a warning message (compiled out in production)
func Warn(format string, v ...interface{}) {
	if IsProduction() {
		return
	}
	log.Printf("[WARN] "+format, v...)
}

// Error logs an error message (compiled out in production)
func Error(format string, v ...interface{}) {
	if IsProduction() {
		return
	}
	log.Printf("[ERROR] "+format, v...)
}

// Fatal logs an error message and exits (only works in development)
func Fatal(format string, v ...interface{}) {
	if IsProduction() {
		os.Exit(1)
	}
	log.Fatalf("[FATAL] "+format, v...)
}

// Printf is a compatibility wrapper (compiled out in production)
func Printf(format string, v ...interface{}) {
	Info(format, v...)
}
