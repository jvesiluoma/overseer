package winplat

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FFmpeg's ddagrab captures output N of the D3D11 device's adapter, so a monitor has to be
// translated into (DXGI adapter index, output index). This walks IDXGIFactory1 the same way.

var (
	dxgi                   = windows.NewLazySystemDLL("dxgi.dll")
	procCreateDXGIFactory1 = dxgi.NewProc("CreateDXGIFactory1")
	iidIDXGIFactory1       = windows.GUID{Data1: 0x770aae78, Data2: 0xf26f, Data3: 0x4dba, Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}}
)

// COM vtable slots (IUnknown 0-2, IDXGIObject 3-6).
const (
	vtRelease              = 2
	vtFactoryEnumAdapters1 = 12 // IDXGIFactory1::EnumAdapters1
	vtAdapterEnumOutputs   = 7  // IDXGIAdapter::EnumOutputs
	vtOutputGetDesc        = 7  // IDXGIOutput::GetDesc
	dxgiErrorNotFound      = 0x887A0002
)

// dxgiOutputDesc is DXGI_OUTPUT_DESC.
type dxgiOutputDesc struct {
	DeviceName        [32]uint16
	Left, Top         int32
	Right, Bottom     int32
	AttachedToDesktop int32
	Rotation          uint32
	Monitor           uintptr
}

type comObject struct{ vtbl *[64]uintptr }

func (o *comObject) call(slot int, args ...uintptr) uintptr {
	all := append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	r, _, _ := syscall.SyscallN(o.vtbl[slot], all...)
	return r
}

func (o *comObject) release() { o.call(vtRelease) }

// DuplicationOutput finds the DXGI adapter and output index of a monitor by its GDI name.
func DuplicationOutput(monitorName string) (adapter, output int, err error) {
	if err := procCreateDXGIFactory1.Find(); err != nil {
		return 0, 0, err
	}
	var factory *comObject
	if hr, _, _ := procCreateDXGIFactory1.Call(uintptr(unsafe.Pointer(&iidIDXGIFactory1)), uintptr(unsafe.Pointer(&factory))); hr != 0 {
		return 0, 0, fmt.Errorf("CreateDXGIFactory1 failed: 0x%x", hr)
	}
	defer factory.release()
	for a := 0; ; a++ {
		var ad *comObject
		if hr := factory.call(vtFactoryEnumAdapters1, uintptr(a), uintptr(unsafe.Pointer(&ad))); uint32(hr) == dxgiErrorNotFound {
			break
		} else if hr != 0 {
			return 0, 0, fmt.Errorf("EnumAdapters1 failed: 0x%x", hr)
		}
		for o := 0; ; o++ {
			var out *comObject
			if hr := ad.call(vtAdapterEnumOutputs, uintptr(o), uintptr(unsafe.Pointer(&out))); hr != 0 {
				break
			}
			var desc dxgiOutputDesc
			hr := out.call(vtOutputGetDesc, uintptr(unsafe.Pointer(&desc)))
			out.release()
			if hr == 0 && strings.EqualFold(windows.UTF16ToString(desc.DeviceName[:]), monitorName) {
				ad.release()
				return a, o, nil
			}
		}
		ad.release()
	}
	return 0, 0, fmt.Errorf("%s is not available for screen capture", monitorName)
}
