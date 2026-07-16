// Package redact removes known secret forms before text reaches logs or the UI.
package redact

import (
	"regexp"
	"strings"
)

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization:\s*(?:bearer|oauth)\s+)[^\s]+`),
	regexp.MustCompile(`(?i)(auth[_ .-]?token["']?\s*[:=]\s*["']?)[^"'\s]+`),
	regexp.MustCompile(`(?i)(oauth[_ .-]?token["']?\s*[:=]\s*["']?)[^"'\s]+`),
	regexp.MustCompile(`(?i)(proxy_pass(?:word)?["']?\s*[:=]\s*["']?)[^"'\s]+`),
	regexp.MustCompile(`(?i)(cookie:\s*)[^\r\n]+`),
}

// Text returns a bounded redacted string.
func Text(value string) string {
	for _, pattern := range patterns {
		value = pattern.ReplaceAllString(value, `${1}[REDACTED]`)
	}
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if strings.Contains(line, "olcrtc://") && strings.Contains(line, "#") {
			lines[i] = "[REDACTED URI]"
		}
	}
	return strings.Join(lines, "\n")
}
