package metricstransport

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Darwin extended ACLs can grant access not shown in the Unix mode, so
// reject any extended security record on a credential or its directories.
func noExtendedACL(fd int) bool {
	attrs := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var result [3]uint32 // total length, attribute reference offset, attribute length
	_, _, errno := syscall.Syscall6(syscall.SYS_FGETATTRLIST, uintptr(fd), uintptr(unsafe.Pointer(&attrs)),
		uintptr(unsafe.Pointer(&result)), unsafe.Sizeof(result), unix.FSOPT_REPORT_FULLSIZE, 0)
	return errno == 0 && result[0] == uint32(unsafe.Sizeof(result)) && result[2] == 0
}
