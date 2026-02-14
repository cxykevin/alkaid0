//go:build windows

package windows

import (
	"errors"
	"unsafe"
)

var (
	procCryptProtectData   = dllCrypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = dllCrypt32.NewProc("CryptUnprotectData")
	procLocalFree          = dllKernel32.NewProc("LocalFree")
)

// CryptProtect flags
const (
	CryptProtectLocalMachine = 0x00000004
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newDataBlob(data []byte) *dataBlob {
	if len(data) == 0 {
		return &dataBlob{}
	}
	return &dataBlob{
		cbData: uint32(len(data)),
		pbData: &data[0],
	}
}

func readDataBlob(blob *dataBlob) []byte {
	if blob == nil || blob.cbData == 0 || blob.pbData == nil {
		return nil
	}
	data := make([]byte, blob.cbData)
	copy(data, unsafe.Slice(blob.pbData, blob.cbData))
	return data
}

func localFree(ptr uintptr) {
	if ptr != 0 {
		procLocalFree.Call(ptr)
	}
}

// LibCryptProtectData encrypts data using DPAPI.
func LibCryptProtectData(data, entropy []byte, flags uint32) ([]byte, error) {
	var out dataBlob
	dataIn := newDataBlob(data)
	entropyIn := newDataBlob(entropy)

	ret, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(dataIn)),
		0,
		uintptr(unsafe.Pointer(entropyIn)),
		0,
		0,
		uintptr(flags),
		uintptr(unsafe.Pointer(&out)),
	)
	if ret == 0 {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("CryptProtectData failed")
	}
	defer localFree(uintptr(unsafe.Pointer(out.pbData)))
	return readDataBlob(&out), nil
}

// LibCryptUnprotectData decrypts data using DPAPI.
func LibCryptUnprotectData(data, entropy []byte, flags uint32) ([]byte, error) {
	var out dataBlob
	dataIn := newDataBlob(data)
	entropyIn := newDataBlob(entropy)

	ret, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(dataIn)),
		0,
		uintptr(unsafe.Pointer(entropyIn)),
		0,
		0,
		uintptr(flags),
		uintptr(unsafe.Pointer(&out)),
	)
	if ret == 0 {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("CryptUnprotectData failed")
	}
	defer localFree(uintptr(unsafe.Pointer(out.pbData)))
	return readDataBlob(&out), nil
}
