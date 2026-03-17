// Package logger provides unified logging with file and console output support.
package logger

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Config holds logger configuration.
type Config struct {
	// LogFile is the path to the log file. If empty, logs only to console.
	LogFile string

	// LogDir is the directory for log files. Used if LogFile is not set but logging to file is enabled.
	LogDir string

	// LogFileName is the name of the log file when using LogDir.
	LogFileName string

	// EnableConsole output to stdout.
	EnableConsole bool

	// EnableFile output to file.
	EnableFile bool

	// Prefix for log messages.
	Prefix string
}

// DefaultConfig returns default logger configuration.
func DefaultConfig() Config {
	return Config{
		EnableConsole: true,
		EnableFile:    false,
		Prefix:        "",
	}
}

// Logger wraps standard logger with file output support.
type Logger struct {
	*log.Logger
	config  Config
	file    *os.File
	mu      sync.Mutex
	closers []io.Closer
}

// New creates a new Logger with the given configuration.
func New(cfg Config) (*Logger, error) {
	l := &Logger{
		config:  cfg,
		closers: make([]io.Closer, 0),
	}

	var writers []io.Writer

	// Console output
	if cfg.EnableConsole {
		writers = append(writers, os.Stdout)
	}

	// File output
	if cfg.EnableFile {
		fileWriter, err := l.createFileWriter(cfg)
		if err != nil {
			return nil, err
		}
		writers = append(writers, fileWriter)
	}

	// Default to stdout if no writers configured
	if len(writers) == 0 {
		writers = append(writers, os.Stdout)
	}

	// Combine all writers
	multiWriter := io.MultiWriter(writers...)
	l.Logger = log.New(multiWriter, cfg.Prefix, log.LstdFlags|log.Lshortfile)

	return l, nil
}

// createFileWriter creates a file writer based on configuration.
func (l *Logger) createFileWriter(cfg Config) (io.Writer, error) {
	logPath := cfg.LogFile

	// If LogFile not set but LogDir is, construct path
	if logPath == "" && cfg.LogDir != "" {
		if err := os.MkdirAll(cfg.LogDir, 0755); err != nil {
			return nil, err
		}
		fileName := cfg.LogFileName
		if fileName == "" {
			fileName = "app.log"
		}
		logPath = filepath.Join(cfg.LogDir, fileName)
	}

	if logPath == "" {
		return nil, nil
	}

	// Ensure directory exists
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	// Open file in append mode
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	l.file = file
	l.closers = append(l.closers, file)

	return file, nil
}

// Close closes all opened file handles.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var lastErr error
	for _, closer := range l.closers {
		if err := closer.Close(); err != nil {
			lastErr = err
		}
	}
	l.closers = nil
	l.file = nil

	return lastErr
}

// Global default logger instance.
var (
	defaultLogger *Logger
	once          sync.Once
	initErr       error
)

// Init initializes the default logger with the given configuration.
// This should be called once at application startup.
func Init(cfg Config) error {
	once.Do(func() {
		defaultLogger, initErr = New(cfg)
	})
	return initErr
}

// Default returns the default logger instance.
// Must call Init() first, otherwise returns a logger with default config.
func Default() *Logger {
	if defaultLogger != nil {
		return defaultLogger
	}
	// Fallback to stdout logger
	l, _ := New(DefaultConfig())
	return l
}

// Close closes the default logger.
func Close() error {
	if defaultLogger != nil {
		return defaultLogger.Close()
	}
	return nil
}

// Convenience functions that delegate to default logger.

// Print calls default logger Print.
func Print(v ...interface{}) {
	Default().Print(v...)
}

// Printf calls default logger Printf.
func Printf(format string, v ...interface{}) {
	Default().Printf(format, v...)
}

// Println calls default logger Println.
func Println(v ...interface{}) {
	Default().Println(v...)
}

// Fatal calls default logger Fatal.
func Fatal(v ...interface{}) {
	Default().Fatal(v...)
}

// Fatalf calls default logger Fatalf.
func Fatalf(format string, v ...interface{}) {
	Default().Fatalf(format, v...)
}

// Fatalln calls default logger Fatalln.
func Fatalln(v ...interface{}) {
	Default().Fatalln(v...)
}

// Panic calls default logger Panic.
func Panic(v ...interface{}) {
	Default().Panic(v...)
}

// Panicf calls default logger Panicf.
func Panicf(format string, v ...interface{}) {
	Default().Panicf(format, v...)
}

// Panicln calls default logger Panicln.
func Panicln(v ...interface{}) {
	Default().Panicln(v...)
}

// SetOutput sets the output destination for the default logger.
func SetOutput(w io.Writer) {
	Default().SetOutput(w)
}

// SetPrefix sets the prefix for the default logger.
func SetPrefix(prefix string) {
	Default().SetPrefix(prefix)
}
