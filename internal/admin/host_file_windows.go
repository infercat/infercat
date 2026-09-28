package admin

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func openHostFile(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, ErrHostFile
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.READ_CONTROL, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, ErrHostFile
	}
	f := os.NewFile(uintptr(h), "admin-code")
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		f.Close()
		return nil, ErrHostFile
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || !privateHostACL(sd) {
		f.Close()
		return nil, ErrHostFile
	}
	return f, nil
}
func privateHostACL(sd *windows.SECURITY_DESCRIPTOR) bool {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false
	}
	owner, _, err := sd.Owner()
	if err != nil || !owner.Equals(user.User.Sid) {
		return false
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return false
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, i, &ace) != nil {
			return false
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return false
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(owner) && !sid.IsWellKnown(windows.WinLocalSystemSid) && !sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			return false
		}
	}
	return true
}
