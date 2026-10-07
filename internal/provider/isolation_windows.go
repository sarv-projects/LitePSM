//go:build windows

package provider

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")

	globalJobObject syscall.Handle
	jobObjectOnce   sync.Once
	jobObjectErr    error
)

const (
	jobObjectExtendedLimitInformationClass = 9
	jobObjectLimitKillOnJobClose           = 0x2000

	processTerminate = 0x0001
	processSetQuota  = 0x0100
)

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func getOrCreateJobObject() (syscall.Handle, error) {
	jobObjectOnce.Do(func() {
		hJob, _, err := procCreateJobObjectW.Call(0, 0)
		if hJob == 0 {
			jobObjectErr = fmt.Errorf("CreateJobObjectW failed: %w", err)
			return
		}

		info := jobObjectExtendedLimitInformation{
			BasicLimitInformation: jobObjectBasicLimitInformation{
				LimitFlags: jobObjectLimitKillOnJobClose,
			},
		}

		ret, _, err := procSetInformationJobObject.Call(
			hJob,
			uintptr(jobObjectExtendedLimitInformationClass),
			uintptr(unsafe.Pointer(&info)),
			uintptr(unsafe.Sizeof(info)),
		)
		if ret == 0 {
			_ = syscall.CloseHandle(syscall.Handle(hJob))
			jobObjectErr = fmt.Errorf("SetInformationJobObject failed: %w", err)
			return
		}

		globalJobObject = syscall.Handle(hJob)
	})

	return globalJobObject, jobObjectErr
}

func setupProcessIsolation(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// On Windows, create in a new process group
	cmd.SysProcAttr.CreationFlags = syscall.CREATE_NEW_PROCESS_GROUP
}

func postStartProcessIsolation(cmd *exec.Cmd, h *ProviderHandle) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	hJob, err := getOrCreateJobObject()
	if err != nil {
		return err
	}

	hProc, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("OpenProcess failed: %w", err)
	}
	defer syscall.CloseHandle(hProc)

	ret, _, callErr := procAssignProcessToJobObject.Call(uintptr(hJob), uintptr(hProc))
	if ret == 0 {
		return fmt.Errorf("AssignProcessToJobObject failed: %w", callErr)
	}

	return nil
}

func killProcessTree(h *ProviderHandle, gracePeriod time.Duration) error {
	if h.Cmd == nil || h.Cmd.Process == nil {
		return nil
	}

	_ = h.Cmd.Process.Kill()

	if h.waitDone == nil {
		time.Sleep(gracePeriod)
		_ = h.Cmd.Process.Kill()
		return nil
	}

	select {
	case <-time.After(gracePeriod):
		_ = h.Cmd.Process.Kill()
		// Wait for the single reaper (monitorProcess) to observe the exit.
		<-h.waitDone
	case <-h.waitDone:
	}
	return nil
}

// killGroupNow is a no-op here: there is no process group to signal; the
// caller kills the direct child.
func killGroupNow(pid int) {}
