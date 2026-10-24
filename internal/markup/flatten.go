package markup

import (
	"context"
	"html"
	"net/url"
	"regexp"
	"strings"
)

func flattenSource(src string) (string, error) {
	normalized := strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	protectedCode, err := multilineCodeSpanRanges(context.Background(), []byte(normalized))
	if err != nil {
		return "", err
	}
	var flattenRange func(int, int) string
	var findContainerEnd func(int, int) int
	findContainerEnd = func(start, end int) int {
		depth := 1
		inIndentedCode := false
		for j := start + 1; j < end; j++ {
			trimmed := strings.TrimSpace(lines[j])
			if inIndentedCode && (trimmed == "" || strings.HasPrefix(lines[j], "    ") || strings.HasPrefix(lines[j], "\t")) {
				continue
			}
			inIndentedCode = false
			if len(protectedCode[j]) > 0 {
				continue
			}
			if strings.HasPrefix(lines[j], "    ") || strings.HasPrefix(lines[j], "\t") {
				inIndentedCode = true
				continue
			}
			if ch, width, _, ok := parseFenceOpen(strings.TrimSpace(lines[j])); ok {
				j++
				for j < end && !isFenceClose(strings.TrimSpace(lines[j]), ch, width) {
					j++
				}
				continue
			}
			if containerStart.MatchString(lines[j]) {
				depth++
			}
			if strings.TrimSpace(lines[j]) == ":::" {
				depth--
				if depth == 0 {
					return j
				}
			}
		}
		return -1
	}
	flattenRange = func(start, end int) string {
		var out []string
		inIndentedCode := false
		for i := start; i < end; {
			line := lines[i]
			trim := strings.TrimSpace(line)
			if inIndentedCode && (trim == "" || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")) {
				out = append(out, line)
				i++
				continue
			}
			inIndentedCode = false
			if ranges := protectedCode[i]; len(ranges) > 0 {
				var lineOut strings.Builder
				last := 0
				for _, span := range ranges {
					lineOut.WriteString(flattenDirective(line[last:span[0]]))
					lineOut.WriteString(line[span[0]:span[1]])
					last = span[1]
				}
				lineOut.WriteString(flattenDirective(line[last:]))
				out = append(out, lineOut.String())
				i++
				continue
			}
			if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
				inIndentedCode = true
				out = append(out, line)
				i++
				continue
			}
			if ch, width, lang, ok := parseFenceOpen(trim); ok {
				j := i + 1
				for j < end && !isFenceClose(strings.TrimSpace(lines[j]), ch, width) {
					j++
				}
				if j == end {
					out = append(out, strings.Join(lines[i:end], "\n"))
					break
				}
				if strings.EqualFold(strings.TrimSpace(lang), "table") {
					table, valid, _ := renderTable(strings.Join(lines[i+1:j], "\n"), i+2)
					if valid {
						out = append(out, tableToPipe(table))
					} else {
						out = append(out, strings.Join(lines[i:j+1], "\n"))
					}
				} else {
					out = append(out, strings.Join(lines[i:j+1], "\n"))
				}
				i = j + 1
				continue
			}
			if m := containerStart.FindStringSubmatch(line); m != nil && knownContainers[m[1]] {
				closeAt := findContainerEnd(i, end)
				if closeAt < 0 {
					out = append(out, line)
					i++
					continue
				}
				name, attrs := m[1], parseAttrs(m[2])
				if name == "products" || name == "posts" {
					out = append(out, strings.Join(lines[i:closeAt+1], "\n"))
					i = closeAt + 1
					continue
				}
				body := flattenRange(i+1, closeAt)
				if name == "item" || name == "tab" {
					title := strings.TrimSpace(attrs["title"])
					if name == "item" {
						body = "**" + title + "**\n\n" + body
					} else {
						body = "### " + title + "\n\n" + body
					}
				}
				if name == "col" {
					out = append(out, "", body, "")
				} else if name == "tab" || name == "item" {
					out = append(out, body+"\n")
				} else {
					out = append(out, body)
				}
				i = closeAt + 1
				continue
			}
			if trim == ":::" {
				i++
				continue
			}
			if strings.Contains(trim, "<!--more-->") {
				line = strings.ReplaceAll(line, "<!--more-->", "")
			}
			line = flattenDirective(line)
			if m := calloutOpenRE.FindStringSubmatch(trim); m != nil {
				kind := strings.ToUpper(m[1][:1]) + strings.ToLower(m[1][1:])
				var body []string
				if strings.TrimSpace(m[2]) != "" {
					body = append(body, strings.TrimSpace(m[2]))
				}
				j := i + 1
				for j < end && strings.HasPrefix(strings.TrimSpace(lines[j]), ">") {
					body = append(body, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[j]), ">")))
					j++
				}
				quote := "> **" + kind + "**"
				if len(body) > 0 {
					quote += "\n>\n> " + strings.Join(body, "\n> ")
				}
				out = append(out, quote)
				i = j
				continue
			}
			line = stripInlineAttrs(line)
			out = append(out, line)
			i++
		}
		return trimFlattenOutput(strings.Join(out, "\n"))
	}
	return stripJMDLinkAttributes(flattenRange(0, len(lines))), nil
}

func trimFlattenOutput(source string) string {
	lines := strings.Split(source, "\n")
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if start == end {
		return ""
	}
	first := lines[start]
	if !strings.HasPrefix(first, "    ") && !strings.HasPrefix(first, "\t") {
		lines[start] = strings.TrimLeft(first, " \t")
	}
	last := lines[end-1]
	if !strings.HasPrefix(last, "    ") && !strings.HasPrefix(last, "\t") {
		lines[end-1] = strings.TrimRight(last, " \t")
	}
	return strings.Join(lines[start:end], "\n")
}

func stripInlineAttrs(s string) string {
	if !headingSourceRE.MatchString(s) {
		return s
	}
	start, end, ok := trailingAttrBlock(s)
	if !ok || !validAttrSyntax(strings.TrimSpace(s[start:end])) {
		return s
	}
	attrs := parseAttrs(strings.TrimSpace(s[start:end]))
	valid := attrs["id"] != "" || strings.TrimSpace(attrs["class"]) != ""
	for key := range motionValues {
		if _, ok := attrs[key]; ok {
			valid = true
		}
	}
	for _, key := range []string{"delay", "dur", "stagger", "once"} {
		if _, ok := attrs[key]; ok {
			valid = true
		}
	}
	if !valid {
		return s
	}
	return strings.TrimRight(s[:start], " \t") + s[end:]
}
func flattenDirective(s string) string {
	return replaceDirectivesOutsideCode(s, func(t string) string {
		m := lineDirective.FindStringSubmatch(t)
		a := parseAttrs("{" + m[2] + "}")
		u := a["url"]
		if u != "" {
			return "[嵌入內容](" + u + ")"
		}
		if m[1] == "compare" {
			var images []string
			for _, side := range []struct{ key, label string }{{"before", "Before"}, {"after", "After"}} {
				if imageURL, ok := safeImageURL(a[side.key]); ok {
					imageURL = strings.NewReplacer("<", "%3C", ">", "%3E", "(", "%28", ")", "%29").Replace(imageURL)
					images = append(images, "!["+side.label+"]("+imageURL+")")
				}
			}
			if len(images) == 0 {
				return "前後比較"
			}
			return strings.Join(images, " ")
		}
		if m[1] == "widget" {
			name := a["name"]
			return "[互動元件](" + url.PathEscape(name) + ")"
		}
		return a["to"]
	})
}
func insideInlineCode(prefix string) bool {
	openRun := 0
	for i := 0; i < len(prefix); {
		if prefix[i] != '`' {
			i++
			continue
		}
		start := i
		for i < len(prefix) && prefix[i] == '`' {
			i++
		}
		if isEscapedAt(prefix, start) {
			continue
		}
		run := i - start
		if openRun == 0 {
			openRun = run
		} else if run == openRun {
			openRun = 0
		}
	}
	return openRun != 0
}
func tableToPipe(s string) string {
	re := regexp.MustCompile(`(?s)<thead><tr>(.*?)</tr></thead><tbody>(.*?)</tbody>`)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	cell := regexp.MustCompile(`<t[hd][^>]*>(.*?)</t[hd]>`)
	headers := cell.FindAllStringSubmatch(m[1], -1)
	var out []string
	var h, sep []string
	for _, x := range headers {
		h = append(h, strings.ReplaceAll(x[1], "|", `\|`))
		if strings.Contains(x[0], `data-align="right"`) {
			sep = append(sep, "---:")
		} else if strings.Contains(x[0], `data-align="center"`) {
			sep = append(sep, ":---:")
		} else if strings.Contains(x[0], `data-align="left"`) {
			sep = append(sep, ":---")
		} else {
			sep = append(sep, "---")
		}
	}
	out = append(out, "| "+strings.Join(h, " | ")+" |", "| "+strings.Join(sep, " | ")+" |")
	for _, row := range regexp.MustCompile(`(?s)<tr>(.*?)</tr>`).FindAllStringSubmatch(m[2], -1) {
		var vals []string
		for _, x := range cell.FindAllStringSubmatch(row[1], -1) {
			vals = append(vals, strings.ReplaceAll(x[1], "|", `\|`))
		}
		out = append(out, "| "+strings.Join(vals, " | ")+" |")
	}
	captionRE := regexp.MustCompile(`(?s)<caption>(.*?)</caption>`)
	if caption := captionRE.FindStringSubmatch(s); len(caption) == 2 && strings.TrimSpace(caption[1]) != "" {
		return "**" + html.UnescapeString(caption[1]) + "**\n\n" + strings.Join(out, "\n")
	}
	return strings.Join(out, "\n")
}
