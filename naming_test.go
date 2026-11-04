package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestExpandOutputTemplate(t *testing.T) {
	t.Setenv("SCREENSHOTCLI_TEST_ROOT", "C:\\Screenshots")
	now := time.Date(2026, 11, 2, 19, 8, 7, 123000000, time.Local)
	got, err := expandOutputTemplate("%SCREENSHOTCLI_TEST_ROOT%\\Screen-[YYYY]-[MM]-[dd]_[HH]-[mm]-[ss]-[MS]-[window-title]-[process].png", `Doc: C:\Work\a*.txt`, "notepad.exe", "[!]", now)
	if err != nil { t.Fatal(err) }
	for _, part := range []string{"C:\\Screenshots", "2026-11-02_19-08-07-123", "Doc[!] C[!][!]Work[!]a[!].txt", "notepad.exe"} {
		if !strings.Contains(got, part) { t.Fatalf("result %q does not contain %q", got, part) }
	}
}

func TestUnknownEnvironmentVariable(t *testing.T) {
	_, err := expandOutputTemplate("%SCREENSHOTCLI_MISSING_VARIABLE%\\a.png", "", "", "[!]", time.Now())
	if err == nil { t.Fatal("expected unknown environment variable error") }
}

func TestReservedWindowsName(t *testing.T) {
	if got := filenameToken("CON", "[!]"); got == "CON" { t.Fatal("reserved Windows name was not sanitized") }
}

func TestReserveOutputPathConflict(t *testing.T) {
	dir := t.TempDir()
	first, err := reserveOutputPath(dir+"\\shot.png", false)
	if err != nil { t.Fatal(err) }
	if err := os.WriteFile(first, []byte("first"), 0644); err != nil { t.Fatal(err) }
	second, err := reserveOutputPath(dir+"\\shot.png", false)
	if err != nil { t.Fatal(err) }
	if !strings.HasSuffix(second, "shot-conflict01.png") { t.Fatalf("unexpected conflict path: %s", second) }
}
