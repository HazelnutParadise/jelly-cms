package markup

import "strings"

// trailingAttrBlock locates a final {...} block while respecting quotes.
func trailingAttrBlock(s string) (int, int, bool) {
	end := len(s)
	for end > 0 && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	if end == 0 || s[end-1] != '}' {
		return 0, 0, false
	}
	depth := 1
	var quote byte
	for i := end - 2; i >= 0; i-- {
		c := s[i]
		if c == '\n' || c == '\r' {
			return 0, 0, false
		}
		if quote != 0 {
			if c == quote {
				slashes := 0
				for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
					slashes++
				}
				if slashes%2 == 0 {
					quote = 0
				}
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '}' {
			depth++
			continue
		}
		if c == '{' {
			depth--
			if depth == 0 {
				return i, end, true
			}
		}
	}
	return 0, 0, false
}

func decodeAttrValue(s string, quote byte) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && (s[i+1] == quote || s[i+1] == '\\') {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
