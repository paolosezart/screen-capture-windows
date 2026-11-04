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

func TestCreateOutputFileConflict(t *testing.T) {
	dir := t.TempDir()
	requested := dir + string(os.PathSeparator) + "shot.png"
	firstFile, firstPath, err := createOutputFile(requested, false)
	if err != nil { t.Fatal(err) }
	if _, err := firstFile.Write([]byte("first")); err != nil { t.Fatal(err) }
	if err := firstFile.Close(); err != nil { t.Fatal(err) }
	secondFile, secondPath, err := createOutputFile(requested, false)
	if err != nil { t.Fatal(err) }
	defer secondFile.Close()
	if !strings.HasSuffix(secondPath, "shot-conflict01.png") { t.Fatalf("unexpected conflict path: %s", secondPath) }
	if firstPath == secondPath { t.Fatal("conflict path must be unique") }
}

func TestCreateOutputFileOverwrite(t *testing.T) {
	path := t.TempDir() + string(os.PathSeparator) + "shot.png"
	if err := os.WriteFile(path, []byte("old data"), 0600); err != nil { t.Fatal(err) }
	f, got, err := createOutputFile(path, true)
	if err != nil { t.Fatal(err) }
	defer f.Close()
	if got != path { t.Fatalf("got %q, want %q", got, path) }
	if info, err := f.Stat(); err != nil || info.Size() != 0 { t.Fatalf("file was not truncated: info=%v err=%v", info, err) }
}
