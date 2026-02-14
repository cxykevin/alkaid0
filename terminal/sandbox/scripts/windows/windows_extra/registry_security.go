//go:build windows

package windows

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var procRegSetKeySecurity = dllAdvapi.NewProc("RegSetKeySecurity")

// SECURITY_INFORMATION flags
const (
	securityInformationDacl          = 0x00000004
	securityInformationProtectedDacl = 0x80000000
)

func buildRegistryDACL() (*windows.ACL, error) {
	sid, _, _, err := windows.LookupSID("", "alk-sandbox$")
	if err != nil {
		return nil, err
	}
	wksids, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, err
	}
	var currToken windows.Token
	if err = windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &currToken); err != nil {
		return nil, err
	}
	defer currToken.Close()
	usr, err := currToken.GetTokenUser()
	if err != nil {
		return nil, err
	}
	currSID := usr.User.Sid

	access := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL | windows.GENERIC_EXECUTE | windows.GENERIC_READ | windows.GENERIC_WRITE,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
			Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		},
		{
			AccessPermissions: windows.GENERIC_ALL | windows.GENERIC_EXECUTE | windows.GENERIC_READ | windows.GENERIC_WRITE,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(wksids),
			},
			Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		},
		{
			AccessPermissions: windows.GENERIC_ALL | windows.GENERIC_EXECUTE | windows.GENERIC_READ | windows.GENERIC_WRITE,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(currSID),
			},
			Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		},
	}
	return windows.ACLFromEntries(access, nil)
}

// SetRegistryKeyDACL applies a protected DACL to the registry key.
func SetRegistryKeyDACL(key registry.Key) error {
	dacl, err := buildRegistryDACL()
	if err != nil {
		return err
	}
	var sd *windows.SECURITY_DESCRIPTOR
	sd, err = windows.NewSecurityDescriptor()
	if err != nil {
		return err
	}
	err = sd.SetDACL(dacl, true, false)
	if err != nil {
		return err
	}
	ret, _, callErr := procRegSetKeySecurity.Call(
		uintptr(key),
		uintptr(securityInformationDacl|securityInformationProtectedDacl),
		uintptr(unsafe.Pointer(sd)),
	)
	if ret != 0 {
		if callErr != nil {
			return callErr
		}
		return errors.New("RegSetKeySecurity failed")
	}
	return nil
}
