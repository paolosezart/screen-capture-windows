package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var envPattern = regexp.MustCompile("%([^%]+)%")
var templatePattern = regexp.MustCompile("\\[(YYYY|MM|dd|HH|mm|ss|MS|timestamp|window-title|Windows title|Windows tittle|process)\\]")

// filenameToken replaces characters forbidden by Windows or Linux with a visible ASCII marker.
func filenameToken(value, replacement string) string {
	var b strings.Builder
	for _, r := range value {
		if r < 32 || strings.ContainsRune(`<>:"/\\|?*`, r) {
			b.WriteString(replacement)
		} else {
			b.WriteRune(r)
		}
	}
	value = strings.TrimRight(b.String(), " .")
	if value == "" || value == "." || value == ".." {
		return "unnamed"
	}
	base := strings.ToUpper(strings.TrimSuffix(value, filepath.Ext(value)))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		value = "_" + value
	}
	return value
}

// expandOutputTemplate expands %ENV% and supported [tokens] using one timestamp.
func expandOutputTemplate(input, title, process, replacement string, now time.Time) (string, error) {
	if replacement == "" {
		return "", fmt.Errorf("--sanitize-char не может быть пустым")
	}
	var missing string
	expanded := envPattern.ReplaceAllStringFunc(input, func(token string) string {
		name := token[1 : len(token)-1]
		value, ok := os.LookupEnv(name)
		if !ok {
			missing = name
			return token
		}
		return value
	})
	if missing != "" {
		return "", fmt.Errorf("переменная окружения %%%s%% не определена", missing)
	}
	values := map[string]string{
		"YYYY": now.Format("2006"), "MM": now.Format("01"), "dd": now.Format("02"),
		"HH": now.Format("15"), "mm": now.Format("04"), "ss": now.Format("05"),
		"MS": now.Format("000"), "timestamp": fmt.Sprintf("%d", now.Unix()),
		"window-title": filenameToken(title, replacement),
		"Windows title": filenameToken(title, replacement),
		"Windows tittle": filenameToken(title, replacement),
		"process": filenameToken(process, replacement),
	}
	return templatePattern.ReplaceAllStringFunc(expanded, func(token string) string {
		key := token[1 : len(token)-1]
		return values[key]
	}), nil
}

// reserveOutputPath atomically reserves a unique output filename. Caller must remove
// the placeholder if it later decides not to write the file.
func reserveOutputPath(requested string, overwrite bool) (string, error) {
	absolute, err := filepath.Abs(requested)
	if err != nil {
		return "", err
	}
	if overwrite {
		return absolute, nil
	}
	dir, name := filepath.Dir(absolute), filepath.Base(absolute)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 0; ; n++ {
		candidate := absolute
		if n > 0 {
			candidate = filepath.Join(dir, fmt.Sprintf("%s-conflict%02d%s", stem, n, ext))
		}
		f, createErr := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if createErr == nil {
			if closeErr := f.Close(); closeErr != nil {
				_ = os.Remove(candidate)
				return "", closeErr
			}
			return candidate, nil
		}
		if !os.IsExist(createErr) {
			return "", createErr
		}
	}
}
