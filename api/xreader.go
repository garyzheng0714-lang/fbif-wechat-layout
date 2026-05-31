package main

import (
	"regexp"
	"strings"
)

func cleanXReaderMarkdown(output string) (content, title string) {
	var cleaned []string
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if title == "" && strings.HasPrefix(trimmed, "# ") {
			title = normalizeXReaderTitle(strings.TrimPrefix(trimmed, "# "))
		}
		if title == "" {
			if manualTitle := xReaderManualTitle(trimmed); manualTitle != "" {
				title = manualTitle
			}
		}
		if xReaderMetadataLine(trimmed) {
			continue
		}
		cleaned = append(cleaned, cleanXReaderBodyLine(line))
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n")), title
}

func xReaderMetadataLine(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	if xReaderManualTitle(trimmed) != "" {
		return true
	}
	if trimmed == "Markdown Content:" {
		return true
	}
	if strings.HasPrefix(trimmed, "URL Source:") || strings.HasPrefix(trimmed, "Published Time:") {
		return true
	}
	if reHTTPURL.MatchString(trimmed) {
		return true
	}
	if strings.HasPrefix(trimmed, "Author:") || strings.HasPrefix(trimmed, "Published:") ||
		strings.HasPrefix(trimmed, "Source:") {
		return true
	}
	return strings.Contains(trimmed, "Author:") && strings.Contains(trimmed, "Published:")
}

func xReaderManualTitle(trimmed string) string {
	idx := strings.Index(trimmed, "Title:")
	if idx < 0 {
		return ""
	}
	prefix := strings.ToLower(trimmed[:idx])
	if !strings.Contains(prefix, "manual") {
		return ""
	}
	return normalizeXReaderTitle(trimmed[idx+len("Title:"):])
}

func normalizeXReaderTitle(raw string) string {
	title := strings.TrimSpace(raw)
	for _, marker := range []string{"–FoodTalks", "-FoodTalks", "—FoodTalks"} {
		if idx := strings.LastIndex(title, marker); idx > 0 {
			title = strings.TrimSpace(title[:idx])
		}
	}
	return title
}

func cleanXReaderBodyLine(line string) string {
	for strings.Contains(line, `\_`) {
		line = strings.ReplaceAll(line, `\_`, "_")
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "_") && strings.HasSuffix(trimmed, "_") {
		unwrapped := strings.TrimSpace(strings.Trim(trimmed, "_"))
		if unwrapped != "" {
			return strings.Replace(line, trimmed, unwrapped, 1)
		}
	}
	return line
}

var reHTTPURL = regexp.MustCompile(`^https?://\S+$`)
