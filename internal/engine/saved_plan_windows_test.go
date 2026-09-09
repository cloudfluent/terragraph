//go:build windows

package engine

import (
	"errors"
	"github.com/cloudfluent/terragraph/internal/privatefs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func planPathFixture(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(t.TempDir(), ".terragraph")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(parent, "plans", "node.tfplan")
}

func assertPrivatePlanACL(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("plan ACL inherits permissions")
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if acl == nil || acl.AceCount != 1 {
		t.Fatalf("got DACL = %+v, want one owner ACE", acl)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !sid.Equals(user.User.Sid) {
		t.Fatal("plan ACL grants another principal access")
	}
}

func TestPrepareSavedPlan_WindowsProtectsDirectoryAndFileBeforeWrite(t *testing.T) {
	path := planPathFixture(t)
	cleanup, err := prepareSavedPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	assertPrivatePlanACL(t, filepath.Dir(path))
	assertPrivatePlanACL(t, path)
	if err := os.WriteFile(path, []byte("synthetic sensitive value"), 0600); err != nil {
		t.Fatal(err)
	}
	assertPrivatePlanACL(t, path)
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("plan remains: %v", err)
	}
}

func TestPrepareSavedPlan_WindowsTightensExistingDirectory(t *testing.T) {
	path := planPathFixture(t)
	dir := filepath.Dir(path)
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}
	cleanup, err := prepareSavedPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	assertPrivatePlanACL(t, dir)
	assertPrivatePlanACL(t, path)
}

func TestPrepareSavedPlan_WindowsRefusesWritableParent(t *testing.T) {
	path := planPathFixture(t)
	parent := filepath.Dir(filepath.Dir(path))
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSavedPlan(path); err == nil || !strings.Contains(err.Error(), "private checkout") {
		t.Fatalf("got = %v, want writable parent refusal", err)
	}
}

func TestPrepareSavedPlan_WindowsLeavesHardlinkTargetUntouched(t *testing.T) {
	path := planPathFixture(t)
	target := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(target, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, path); err != nil {
		t.Fatal(err)
	}
	cleanup, err := prepareSavedPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := os.WriteFile(path, []byte("new plan"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatalf("got = %q, want keep", got)
	}
	after, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if before.String() != after.String() {
		t.Fatal("protecting the plan changed the hardlink target ACL")
	}
}

func TestPrepareSavedPlan_WindowsRefusesPlanDirectorySymlink(t *testing.T) {
	path := planPathFixture(t)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Dir(path)); err != nil {
		if os.IsPermission(err) || errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
			t.Skip("creating Windows symlinks requires developer mode or elevated privileges")
		}
		t.Fatal(err)
	}
	before, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSavedPlan(path); err == nil {
		t.Fatal("expected directory symlink refusal")
	}
	after, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if before.String() != after.String() {
		t.Fatal("refusing the symlink changed its target ACL")
	}
}

func TestPrepareSavedPlan_WindowsRefusesWritableExistingPlanDirectory(t *testing.T) {
	path := planPathFixture(t)
	dir := filepath.Dir(path)
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSavedPlan(path); err == nil || !strings.Contains(err.Error(), "private checkout") {
		t.Fatalf("got = %v, want existing writable directory refusal", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("plan created: %v", err)
	}
}

func TestCreatePlanDirectory_WindowsDoesNotInheritWriteACL(t *testing.T) {
	path := planPathFixture(t)
	parent := filepath.Dir(filepath.Dir(path))
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;CIIO;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err := privatefs.Directory(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	assertPrivatePlanACL(t, filepath.Dir(path))
	cleanup, err := prepareSavedPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	assertPrivatePlanACL(t, path)
}
