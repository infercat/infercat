package admin

import (
	"golang.org/x/sys/windows"
	"testing"
)

func TestHostFileWindowsACL(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	owner := user.User.Sid.String()
	for _, tc := range []struct {
		acl  string
		want bool
	}{
		{"D:P(A;;FA;;;" + owner + ")", true},
		{"D:P(A;;FA;;;" + owner + ")(A;;FA;;;SY)(A;;FA;;;BA)", true},
		{"D:P(A;;FA;;;" + owner + ")(A;;GR;;;WD)", false},
		{"D:NO_ACCESS_CONTROL", false},
	} {
		sd, err := windows.SecurityDescriptorFromString("O:" + owner + tc.acl)
		if err != nil {
			t.Fatal(err)
		}
		if got := privateHostACL(sd); got != tc.want {
			t.Errorf("ACL private=%v want %v", got, tc.want)
		}
	}
}
