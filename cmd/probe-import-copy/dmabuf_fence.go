package main

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// dma-buf fence ioctls from the Linux UAPI (linux/dma-buf.h). They let
// userspace read the fences attached to a dma-buf and publish its own, which is
// the mechanism an explicit-synchronization consumer needs to interoperate with
// a dma-buf producer. Both take an 8-byte struct: a u32 flags field and an s32
// descriptor field.
const (
	dmaBufSyncRead  = 1 << 0
	dmaBufSyncWrite = 1 << 1

	dmaBufIoctlExportSyncFile = 0xC0086202 // _IOWR('b', 2, struct dma_buf_export_sync_file)
	dmaBufIoctlImportSyncFile = 0x40086203 // _IOW('b', 3, struct dma_buf_import_sync_file)
)

// dmaBufFenceResult reports what the kernel and driver expose for one dma-buf.
//
// This is a mechanism check only. It answers "can a consumer read a completion
// fence from this dma-buf, and can it publish its own", not "does a particular
// producer publish one".
type dmaBufFenceResult struct {
	Stage string `json:"stage"`

	ExportReadSupported bool   `json:"export_read_supported"`
	ExportReadErrno     string `json:"export_read_errno,omitempty"`
	ExportReadFD        int    `json:"export_read_fd"`
	ExportReadSignaled  bool   `json:"export_read_signaled"`
	ExportReadWaitMS    int64  `json:"export_read_wait_ms"`

	ExportWriteSupported bool   `json:"export_write_supported"`
	ExportWriteErrno     string `json:"export_write_errno,omitempty"`
	ExportWriteFD        int    `json:"export_write_fd"`
	ExportWriteSignaled  bool   `json:"export_write_signaled"`
	ExportWriteWaitMS    int64  `json:"export_write_wait_ms"`

	// Publishing a fence back is reported twice: into the same buffer, which the
	// kernel may reject as a self-reference, and into a scratch buffer, which is
	// the honest test of driver support.
	ImportSelfErrno    string `json:"import_self_errno,omitempty"`
	ImportScratchErrno string `json:"import_scratch_errno,omitempty"`
	ImportScratchOK    bool   `json:"import_scratch_ok"`

	Note string `json:"note,omitempty"`
}

// probeDMABUFFence exports the dma-buf's current fences as sync files, waits on
// each with a bounded timeout, then imports the read fence back as a read
// fence. Every step reports its own errno so an unsupported driver is visible
// instead of being mistaken for "no fences".
func probeDMABUFFence(fd int, stage string, wait time.Duration) dmaBufFenceResult {
	result := dmaBufFenceResult{Stage: stage, ExportReadFD: -1, ExportWriteFD: -1}
	if fd < 0 {
		result.Note = "no dma-buf descriptor available for this stage"
		return result
	}

	readFD, readErr := exportSyncFile(fd, dmaBufSyncRead)
	switch {
	case readErr != nil:
		result.ExportReadErrno = errnoName(readErr)
	case readFD < 0:
		result.ExportReadSupported = true
		result.Note = "export succeeded with no fence pending (descriptor -1)"
	default:
		result.ExportReadSupported = true
		result.ExportReadFD = readFD
		signaled, waited := waitForSyncFile(readFD, wait)
		result.ExportReadSignaled = signaled
		result.ExportReadWaitMS = waited.Milliseconds()
		_ = unix.Close(readFD)
	}

	writeFD, writeErr := exportSyncFile(fd, dmaBufSyncWrite)
	switch {
	case writeErr != nil:
		result.ExportWriteErrno = errnoName(writeErr)
	case writeFD < 0:
		result.ExportWriteSupported = true
	default:
		result.ExportWriteSupported = true
		result.ExportWriteFD = writeFD
		signaled, waited := waitForSyncFile(writeFD, wait)
		result.ExportWriteSignaled = signaled
		result.ExportWriteWaitMS = waited.Milliseconds()
		_ = unix.Close(writeFD)
	}

	// Publishing a fence is the other half of the contract: it is how a consumer
	// tells an implicitly synchronised producer to wait for its read. Re-importing
	// into the same buffer is a self-reference, so a scratch buffer distinguishes
	// "driver does not implement it" from "rejected in this case".
	if readErr == nil && readFD >= 0 {
		result.ImportSelfErrno = errnoName(importSyncFile(fd, dmaBufSyncRead, readFD))
		scratchFD, _, _, scratchDRMFD, allocErr := allocateDMABUF(1, 1, 32, []byte{0, 0, 0, 255})
		if allocErr != nil {
			result.ImportScratchErrno = errnoName(allocErr)
		} else {
			if err := importSyncFile(scratchFD, dmaBufSyncRead, readFD); err != nil {
				result.ImportScratchErrno = errnoName(err)
			} else {
				result.ImportScratchOK = true
			}
			releaseDMABUF(scratchFD, scratchDRMFD)
		}
		_ = unix.Close(readFD)
	} else if readErr == nil {
		// Nothing pending: publish nothing rather than claiming success for an
		// operation that had no fence to insert.
		result.Note = joinNote(result.Note, "no read fence to publish")
	}
	return result
}

func exportSyncFile(fd int, flags uint32) (int, error) {
	arg := struct {
		flags uint32
		fd    int32
	}{flags: flags, fd: -1}
	if err := ioctl(fd, dmaBufIoctlExportSyncFile, unsafe.Pointer(&arg)); err != nil {
		return -1, err
	}
	return int(arg.fd), nil
}

func importSyncFile(fd int, flags uint32, syncFD int) error {
	arg := struct {
		flags uint32
		fd    int32
	}{flags: flags, fd: int32(syncFD)}
	return ioctl(fd, dmaBufIoctlImportSyncFile, unsafe.Pointer(&arg))
}

func ioctl(fd int, request uintptr, arg unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), request, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}

// waitForSyncFile polls a sync file for completion. A sync file reports
// POLLIN when the fence it carries has signaled.
func waitForSyncFile(syncFD int, timeout time.Duration) (bool, time.Duration) {
	deadline := time.Now().Add(timeout)
	pollFDs := []unix.PollFd{{Fd: int32(syncFD), Events: unix.POLLIN}}
	start := time.Now()
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, time.Since(start)
		}
		ms := int(remaining.Milliseconds())
		if ms < 1 {
			ms = 1
		}
		ready, err := unix.Poll(pollFDs, ms)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return false, time.Since(start)
		}
		if ready > 0 {
			return true, time.Since(start)
		}
		return false, time.Since(start)
	}
}

// dupDMABUFFD returns a close-on-exec duplicate so the fence probe can query the
// buffer after the GL texture adopted the original descriptor.
func dupDMABUFFD(fd int) int {
	dup, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return -1
	}
	return dup
}

func errnoName(err error) string {
	var errno unix.Errno
	if errors.As(err, &errno) {
		return errno.Error()
	}
	return err.Error()
}

func joinNote(existing, extra string) string {
	switch {
	case existing == "":
		return extra
	case extra == "":
		return existing
	default:
		return fmt.Sprintf("%s; %s", existing, extra)
	}
}
