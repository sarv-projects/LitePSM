package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/sarv-projects/litepsm/internal/domain"
)

const (
	// MaxStderrBufferSize is the maximum size (64 KiB) of the rotating in-memory stderr buffer per provider.
	MaxStderrBufferSize = 64 * 1024

	// DefaultStartupTimeout is the maximum time to wait for a provider to respond to initialization.
	DefaultStartupTimeout = 30 * time.Second
)

// RingBuffer stores a bounded rotating log of recent bytes without unbounded allocation.
type RingBuffer struct {
	buf  []byte
	size int
	mu   sync.Mutex
}

// NewRingBuffer creates a new ring buffer with maximum capacity.
func NewRingBuffer(size int) *RingBuffer {
	if size <= 0 {
		size = MaxStderrBufferSize
	}
	return &RingBuffer{
		buf:  make([]byte, 0, size),
		size: size,
	}
}

// Write appends bytes, discarding oldest bytes if capacity is exceeded.
func (r *RingBuffer) Write(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n = len(p)
	if len(p) >= r.size {
		// Overwrite entire buffer with tail of input
		r.buf = append(r.buf[:0], p[len(p)-r.size:]...)
		return n, nil
	}

	overflow := (len(r.buf) + len(p)) - r.size
	if overflow > 0 {
		r.buf = append(r.buf[overflow:], p...)
	} else {
		r.buf = append(r.buf, p...)
	}
	return n, nil
}

// String returns the buffered contents as a UTF-8 string.
func (r *RingBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

// LaunchSpec defines parameters for launching a local or supervised provider process.
type LaunchSpec struct {
	Executable   string            `json:"executable"`
	Args         []string          `json:"args"`
	WorkingDir   string            `json:"workingDir"`
	Env          map[string]string `json:"env"`
	SecretEnv    map[string]string `json:"secretEnv,omitempty"`
	TimeoutSec   int               `json:"timeoutSec"`
}

// ProviderStatus describes runtime lifecycle status.
type ProviderStatus string

const (
	StatusStarting     ProviderStatus = "starting"
	StatusRunning      ProviderStatus = "running"
	StatusUnresponsive ProviderStatus = "unresponsive"
	StatusStopped      ProviderStatus = "stopped"
	StatusError        ProviderStatus = "error"
)

// ProviderHandle tracks an active supervised child process.
type ProviderHandle struct {
	ProviderID     string
	PID            int
	Status         ProviderStatus
	LaunchSpec     LaunchSpec
	StderrBuffer   *RingBuffer
	Cmd            *exec.Cmd
	Stdin          io.WriteCloser
	Stdout         io.ReadCloser
	PipeReader     *io.PipeReader
	PipeWriter     *io.PipeWriter
	StartedAt      time.Time
	StoppedAt      *time.Time
	ExitError      error
	mu             sync.RWMutex
}

// InvocationRequest represents a tool execution request directed to a provider.
type InvocationRequest struct {
	InvocationID string          `json:"invocationId"`
	ProviderID   string          `json:"providerId"`
	CapabilityID string          `json:"capabilityId"`
	ToolName     string          `json:"toolName"`
	Arguments    json.RawMessage `json:"arguments"`
	TimeoutSec   int             `json:"timeoutSec,omitempty"`
}

// InvocationResult represents the response of a tool execution.
type InvocationResult struct {
	InvocationID string          `json:"invocationId"`
	Output       json.RawMessage `json:"output"`
	IsError      bool            `json:"isError"`
	DurationMs   int64           `json:"durationMs"`
}

// Supervisor manages provider lifecycle, process isolation, and zero-orphan cleanup.
type Supervisor struct {
	providers map[string]*ProviderHandle
	mu        sync.RWMutex
}

// NewSupervisor initializes a new supervisor instance.
func NewSupervisor() *Supervisor {
	return &Supervisor{
		providers: make(map[string]*ProviderHandle),
	}
}

// StartProvider spawns a provider process with strict process group isolation and parent death guarantees.
func (s *Supervisor) StartProvider(ctx context.Context, providerID string, spec LaunchSpec) (*ProviderHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.providers[providerID]; ok && existing.Status == StatusRunning {
		return existing, nil
	}

	if spec.Executable == "" {
		return nil, domain.ErrInvalidIdentifier("executable", "non-empty executable path")
	}

	cmd := exec.CommandContext(ctx, spec.Executable, spec.Args...)
	cmd.Dir = spec.WorkingDir

	// Build minimal clean environment
	cmd.Env = os.Environ()
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	for k, v := range spec.SecretEnv {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Platform-specific process isolation
	setupProcessIsolation(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe for %s: %w", providerID, err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("failed to create stdout pipe for %s: %w", providerID, err)
	}

	stderrRing := NewRingBuffer(MaxStderrBufferSize)
	cmd.Stderr = stderrRing

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("failed to start provider process %s: %w", providerID, err)
	}

	handle := &ProviderHandle{
		ProviderID:   providerID,
		PID:          cmd.Process.Pid,
		Status:       StatusRunning,
		LaunchSpec:   spec,
		StderrBuffer: stderrRing,
		Cmd:          cmd,
		Stdin:        stdin,
		Stdout:       stdout,
		StartedAt:    time.Now().UTC(),
	}

	s.providers[providerID] = handle

	// Monitor child process exit asynchronously
	go s.monitorProcess(handle)

	return handle, nil
}

// StopProvider terminates a provider process gracefully with SIGTERM, falling back to SIGKILL.
func (s *Supervisor) StopProvider(ctx context.Context, providerID string) error {
	s.mu.Lock()
	handle, ok := s.providers[providerID]
	if !ok {
		s.mu.Unlock()
		return domain.ErrNotFound("provider", providerID)
	}
	s.mu.Unlock()

	return handle.Terminate(2 * time.Second)
}

// GetProvider retrieves an active or recorded provider handle.
func (s *Supervisor) GetProvider(providerID string) (*ProviderHandle, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	handle, ok := s.providers[providerID]
	if !ok {
		return nil, domain.ErrNotFound("provider", providerID)
	}
	return handle, nil
}

// Terminate stops the underlying process tree safely.
func (h *ProviderHandle) Terminate(gracePeriod time.Duration) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.Cmd == nil || h.Cmd.Process == nil || h.Status == StatusStopped {
		h.Status = StatusStopped
		return nil
	}

	_ = h.Stdin.Close()
	_ = h.Stdout.Close()

	_ = killProcessTree(h, gracePeriod)

	now := time.Now().UTC()
	h.StoppedAt = &now
	h.Status = StatusStopped
	return nil
}

// StderrLogs returns recent stderr logs from the rotating ring buffer.
func (h *ProviderHandle) StderrLogs() string {
	if h.StderrBuffer == nil {
		return ""
	}
	return h.StderrBuffer.String()
}

func (s *Supervisor) monitorProcess(h *ProviderHandle) {
	err := h.Cmd.Wait()
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now().UTC()
	h.StoppedAt = &now
	h.ExitError = err
	if err != nil {
		h.Status = StatusError
	} else {
		h.Status = StatusStopped
	}
}

// StopAll stops all active supervised providers (invoked on daemon shutdown).
func (s *Supervisor) StopAll(ctx context.Context) {
	s.mu.Lock()
	var handles []*ProviderHandle
	for _, h := range s.providers {
		handles = append(handles, h)
	}
	s.mu.Unlock()

	var wg sync.WaitGroup
	for _, h := range handles {
		wg.Add(1)
		go func(handle *ProviderHandle) {
			defer wg.Done()
			_ = handle.Terminate(1 * time.Second)
		}(h)
	}
	wg.Wait()
}
