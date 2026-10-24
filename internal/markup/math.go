package markup

import (
	"context"
	"html"
	"strings"
)

func mathHTML(tex string, block bool, p *pipeline) string {
	if !validInlineMath(tex) {
		return html.EscapeString(tex)
	}
	tag := "span"
	extra := ""
	if block {
		tag = "div"
		extra = ` data-display="block"`
	}
	return p.placeholder(`<` + tag + ` class="jc-math"` + extra + ` data-tex="` + html.EscapeString(tex) + `">` + html.EscapeString(tex) + `</` + tag + `>`)
}
func validInlineMath(s string) bool {
	return s != "" && strings.TrimSpace(s) == s
}
func mathInline(s string, p *pipeline) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if i&0x3fff == 0 && p.ctx.Err() != nil {
			out.WriteString(s[i:])
			return out.String()
		}
		if s[i] == '`' {
			run := i + 1
			for run < len(s) && s[run] == '`' {
				run++
			}
			fence := s[i:run]
			end := strings.Index(s[run:], fence)
			if end >= 0 {
				end += run + len(fence)
				out.WriteString(s[i:end])
				i = end
				continue
			}
			out.WriteString(fence)
			i = run
			continue
		}
		if s[i] == '$' && i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
			if end := findMathClose(s, i+1, p.ctx); end >= 0 {
				out.WriteString(s[i : end+1])
				i = end + 1
				continue
			}
		}
		if s[i] != '$' || isEscapedDollar(s, i) {
			out.WriteByte(s[i])
			i++
			continue
		}
		end := findMathClose(s, i+1, p.ctx)
		if end < 0 {
			out.WriteByte(s[i])
			i++
			continue
		}
		tex := s[i+1 : end]
		if !validInlineMath(tex) {
			out.WriteString(s[i : end+1])
			i = end + 1
			continue
		}
		escaped := html.EscapeString(tex)
		out.WriteString(p.placeholder(`<span class="jc-math" data-tex="` + escaped + `">` + escaped + `</span>`))
		i = end + 1
	}
	return out.String()
}

func findMathClose(s string, start int, ctx context.Context) int {
	for i := start; i < len(s); i++ {
		if i&0x3fff == 0 && ctx.Err() != nil {
			return -1
		}
		if s[i] == '$' && !isEscapedDollar(s, i) {
			return i
		}
	}
	return -1
}

func isEscapedDollar(s string, i int) bool {
	slashes := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		slashes++
	}
	return slashes%2 == 1
}
