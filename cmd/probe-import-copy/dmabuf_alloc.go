package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// DRM ioctl constants for dumb buffer allocation and dma-buf export.
const (
	drmIoctlModeCreateDumb  = 0xc02064b2
	drmIoctlModeMapDumb     = 0xc01064b3
	drmIoctlPrimeHandleToFD = 0xc00c642d
)

// drmModeCreateDumb mirrors struct drm_mode_create_dumb.
type drmModeCreateDumb struct {
	Height uint32
	Width  uint32
	BPP    uint32
	Flags  uint32
	Handle uint32 // out
	Pitch  uint32 // out
	Size   uint64 // out
}

// drmModeMapDumb mirrors struct drm_mode_map_dumb.
type drmModeMapDumb struct {
	Handle uint32 // in
	Pad    uint32
	Offset uint64 // out
}

// drmPrimeHandle mirrors struct drm_prime_handle.
type drmPrimeHandle struct {
	Handle uint32 // in
	Flags  uint32 // in
	FD     int32  // out
}

// allocateDMABUF allocates a DRM dumb buffer, fills it with the given pixel
// data, and exports it as a dma-buf FD. Returns the FD, pitch (stride in
// bytes), buffer size, and the DRM FD (which must be kept open while the
// dma-buf FD is active on some kernel versions).
//
// Access to /dev/dri/card0 (group video) is required.
func allocateDMABUF(width, height, bpp int32, pixels []byte) (dmaBufFD int, pitch uint32, bufSize int64, drmFD int, err error) {
	drmFD, err = unix.Open("/dev/dri/card0", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, 0, 0, -1, fmt.Errorf("open /dev/dri/card0: %w", err)
	}

	create := drmModeCreateDumb{
		Width:  uint32(width),
		Height: uint32(height),
		BPP:    uint32(bpp),
	}
	if err = drmIoctl(drmFD, drmIoctlModeCreateDumb, unsafe.Pointer(&create)); err != nil {
		unix.Close(drmFD)
		return -1, 0, 0, -1, fmt.Errorf("DRM_IOCTL_MODE_CREATE_DUMB: %w", err)
	}

	// Map the dumb buffer to fill with pixel data.
	mapReq := drmModeMapDumb{Handle: create.Handle}
	if err = drmIoctl(drmFD, drmIoctlModeMapDumb, unsafe.Pointer(&mapReq)); err != nil {
		unix.Close(drmFD)
		return -1, 0, 0, -1, fmt.Errorf("DRM_IOCTL_MODE_MAP_DUMB: %w", err)
	}

	data, err := unix.Mmap(drmFD, int64(mapReq.Offset), int(create.Size),
		unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		unix.Close(drmFD)
		return -1, 0, 0, -1, fmt.Errorf("mmap DRM dumb buffer: %w", err)
	}
	// Fill with pixel data, honoring the pitch (stride).  The pitch may be
	// larger than width*bytesPerPixel; remaining bytes in each row stay zero.
	rowBytes := int32(width) * (bpp / 8)
	pitch32 := int32(create.Pitch)
	for y := int32(0); y < height; y++ {
		srcStart := y * rowBytes
		dstStart := y * pitch32
		copy(data[dstStart:dstStart+rowBytes], pixels[srcStart:srcStart+rowBytes])
	}
	if err := unix.Munmap(data); err != nil {
		unix.Close(drmFD)
		return -1, 0, 0, -1, fmt.Errorf("munmap DRM dumb buffer: %w", err)
	}

	// Export the handle as a dma-buf FD.
	prime := drmPrimeHandle{
		Handle: create.Handle,
		Flags:  uint32(unix.O_CLOEXEC),
	}
	if err = drmIoctl(drmFD, drmIoctlPrimeHandleToFD, unsafe.Pointer(&prime)); err != nil {
		unix.Close(drmFD)
		return -1, 0, 0, -1, fmt.Errorf("DRM_IOCTL_PRIME_HANDLE_TO_FD: %w", err)
	}

	return int(prime.FD), create.Pitch, int64(create.Size), drmFD, nil
}

// releaseDMABUF closes the dma-buf FD and its originating DRM FD.
func releaseDMABUF(dmaBufFD, drmFD int) {
	if dmaBufFD >= 0 {
		_ = unix.Close(dmaBufFD)
	}
	if drmFD >= 0 {
		_ = unix.Close(drmFD)
	}
}

// drmIoctl performs a DRM ioctl call via the raw syscall.
func drmIoctl(fd int, req uintptr, arg unsafe.Pointer) error {
	_, _, err := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
	if err != 0 {
		return err
	}
	return nil
}
