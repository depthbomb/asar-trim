//go:build windows

package optimizer

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	wofProviderFile            = 2
	fileProviderCompressionLZX = 1
	fsctlSetCompression        = 0x0009C040
	compressionFormatDefault   = 1
)

type wofFileCompressionInfo struct {
	Algorithm uint32
	Flags     uint32
}

var (
	wofutilDLL             = syscall.NewLazyDLL("Wofutil.dll")
	wofSetFileDataLocation = wofutilDLL.NewProc("WofSetFileDataLocation")
	kernel32DLL            = syscall.NewLazyDLL("kernel32.dll")
	deviceIoControl        = kernel32DLL.NewProc("DeviceIoControl")
)

func windowsCompressionSupported() bool { return true }

// compressWindowsArchive first requests WOF/CompactOS LZX compression. If the
// filesystem rejects that mode, it falls back to ordinary transparent NTFS
// compression, which uses the filesystem compressed attribute.
func compressWindowsArchive(name string) (string, error) {
	wofErr := setWofLZX(name)
	if wofErr == nil {
		return "wof-lzx", nil
	}
	if err := setNTFSCompression(name); err != nil {
		return "", fmt.Errorf("WOF LZX compression failed (%v); NTFS compression fallback failed: %w", wofErr, err)
	}
	return "ntfs", nil
}

func setWofLZX(name string) error {
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info := wofFileCompressionInfo{Algorithm: fileProviderCompressionLZX}
	hresult, _, _ := wofSetFileDataLocation.Call(f.Fd(), wofProviderFile, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if int32(hresult) < 0 {
		return fmt.Errorf("WofSetFileDataLocation HRESULT 0x%08X", uint32(hresult))
	}
	return nil
}

func setNTFSCompression(name string) error {
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	format := uint16(compressionFormatDefault)
	var returned uint32
	ok, _, callErr := deviceIoControl.Call(
		f.Fd(), fsctlSetCompression,
		uintptr(unsafe.Pointer(&format)), unsafe.Sizeof(format),
		0, 0, uintptr(unsafe.Pointer(&returned)), 0,
	)
	if ok == 0 {
		return callErr
	}
	return nil
}
