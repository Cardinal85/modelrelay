//go:build windows

package certmgr

import (
	"fmt"
	"os"
	"os/user"

	"golang.org/x/sys/windows"
)

func mkdirSecure(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return restrictPath(dir, true)
}

func lockDownDir(dir string) error {
	return restrictPath(dir, true)
}

func chmodPrivate(path string) error {
	return restrictPath(path, false)
}

func chmodPublic(path string) error {
	return restrictPath(path, false)
}

func restrictPath(path string, isDir bool) error {
	u, err := user.Current()
	if err != nil {
		return fmt.Errorf("certmgr: current user: %w", err)
	}
	userSID, err := windows.StringToSid(u.Uid)
	if err != nil {
		return fmt.Errorf("certmgr: user sid: %w", err)
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("certmgr: system sid: %w", err)
	}

	inherit := uint32(windows.NO_INHERITANCE)
	if isDir {
		inherit = uint32(windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT)
	}
	entries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       inherit,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(userSID),
			},
		},
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       inherit,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(systemSID),
			},
		},
	}
	dacl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("certmgr: build acl: %w", err)
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
}
