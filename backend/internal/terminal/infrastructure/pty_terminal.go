package infrastructure

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/kanivet/backend/internal/terminal/domain"
)

// PTYTerminal implements the Terminal interface using pseudo-terminal
type PTYTerminal struct {
	cmd    *exec.Cmd
	pty    *os.File
	mu     sync.Mutex
	closed bool
	reader *bufio.Reader
}

// NewPTYTerminal creates a new PTY-based terminal
func NewPTYTerminal(shell string, size domain.TerminalSize) (*PTYTerminal, error) {
	// Default to user's shell if not specified
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
	}

	// Create command
	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	// Start command with PTY
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: size.Rows,
		Cols: size.Cols,
		X:    0,
		Y:    0,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start pty: %w", err)
	}

	return &PTYTerminal{
		cmd:    cmd,
		pty:    ptmx,
		reader: bufio.NewReader(ptmx),
	}, nil
}

// Start starts the terminal (already started in NewPTYTerminal)
func (t *PTYTerminal) Start() error {
	return nil
}

// Resize changes the terminal size
func (t *PTYTerminal) Resize(size domain.TerminalSize) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return fmt.Errorf("terminal is closed")
	}

	return pty.Setsize(t.pty, &pty.Winsize{
		Rows: size.Rows,
		Cols: size.Cols,
		X:    0,
		Y:    0,
	})
}

// Write sends input to the terminal
func (t *PTYTerminal) Write(data []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return 0, fmt.Errorf("terminal is closed")
	}

	return t.pty.Write(data)
}

// Read reads output from the terminal
func (t *PTYTerminal) Read() ([]byte, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, io.EOF
	}
	t.mu.Unlock()

	// Read up to 4KB at a time
	buf := make([]byte, 4096)
	n, err := t.pty.Read(buf)
	if err != nil {
		return nil, err
	}

	return buf[:n], nil
}

// Close terminates the terminal
func (t *PTYTerminal) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}

	t.closed = true

	// Close PTY
	if err := t.pty.Close(); err != nil {
		return fmt.Errorf("failed to close pty: %w", err)
	}

	// Terminate process
	if t.cmd.Process != nil {
		// Send SIGTERM first
		if err := t.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			// If SIGTERM fails, force kill
			if killErr := t.cmd.Process.Kill(); killErr != nil {
				log.Printf("Failed to kill process: %v", killErr)
			}
		}
	}

	return nil
}

// Wait waits for the terminal process to exit
func (t *PTYTerminal) Wait() error {
	return t.cmd.Wait()
}

// PTYTerminalFactory creates PTY-based terminals
type PTYTerminalFactory struct{}

// NewPTYTerminalFactory creates a new PTY terminal factory
func NewPTYTerminalFactory() *PTYTerminalFactory {
	return &PTYTerminalFactory{}
}

// CreateTerminal creates a new terminal instance
func (f *PTYTerminalFactory) CreateTerminal(shell string, size domain.TerminalSize) (domain.Terminal, error) {
	return NewPTYTerminal(shell, size)
}
