package markup

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"go.yaml.in/yaml/v3"
)

const maxYAMLNodes = 10000
const maxYAMLAliases = 8
const maxYAMLDepth = 32

type tableSpec struct {
	caption   string
	columns   []string
	align     []string
	rows      [][]string
	headerCol bool
}

func renderTable(src string, line int) (string, bool, []Diagnostic) {
	if len(src) > 64*1024 {
		return "", false, []Diagnostic{{Line: line, Col: 1, Severity: "warning", Code: "table_size", Message: "Table exceeds 64 KB"}}
	}
	spec, err := parseTableYAML([]byte(src))
	if err != nil {
		return "", false, []Diagnostic{{Line: line, Col: 1, Severity: "warning", Code: "table_yaml", Message: err.Error()}}
	}
	if len(spec.columns) == 0 {
		return "", false, []Diagnostic{{Line: line, Col: 1, Severity: "warning", Code: "table_yaml", Message: "Table requires columns"}}
	}
	if len(spec.columns) > 20 {
		return "", false, []Diagnostic{{Line: line, Col: 1, Severity: "warning", Code: "table_columns", Message: "Table exceeds 20 columns"}}
	}
	if len(spec.rows) > 500 {
		return "", false, []Diagnostic{{Line: line, Col: 1, Severity: "warning", Code: "table_rows", Message: "Table exceeds 500 rows"}}
	}
	var b strings.Builder
	b.WriteString(`<figure class="jc-table"><table>`)
	if spec.caption != "" {
		b.WriteString(`<caption>` + html.EscapeString(spec.caption) + `</caption>`)
	}
	b.WriteString(`<thead><tr>`)
	for i, c := range spec.columns {
		b.WriteString(`<th scope="col"` + alignAttr(alignAt(spec.align, i)) + `>` + html.EscapeString(c) + `</th>`)
	}
	b.WriteString(`</tr></thead><tbody>`)
	for _, row := range spec.rows {
		b.WriteString(`<tr>`)
		for i := range spec.columns {
			value := ""
			if i < len(row) {
				value = row[i]
			}
			a := alignAttr(alignAt(spec.align, i))
			if spec.headerCol && i == 0 {
				b.WriteString(`<th scope="row"` + a + `>` + html.EscapeString(value) + `</th>`)
			} else {
				b.WriteString(`<td` + a + `>` + html.EscapeString(value) + `</td>`)
			}
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table></figure>`)
	return b.String(), true, nil
}

func parseTableYAML(raw []byte) (tableSpec, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return tableSpec{}, fmt.Errorf("invalid table YAML: %v", err)
	}
	nodes, aliases, maxDepth := 0, 0, 0
	var visit func(*yaml.Node, int)
	visit = func(n *yaml.Node, d int) {
		if n == nil {
			return
		}
		nodes++
		if d > maxDepth {
			maxDepth = d
		}
		if n.Kind == yaml.AliasNode {
			aliases++
		}
		for _, child := range n.Content {
			visit(child, d+1)
		}
	}
	visit(&doc, 0)
	if nodes > maxYAMLNodes || aliases > maxYAMLAliases || maxDepth > maxYAMLDepth {
		return tableSpec{}, fmt.Errorf("table YAML exceeds safe alias or nesting limits")
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return tableSpec{}, fmt.Errorf("table YAML must be a mapping")
	}
	root := doc.Content[0]
	values := map[string]*yaml.Node{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		if key.Kind != yaml.ScalarNode {
			return tableSpec{}, fmt.Errorf("table YAML keys must be strings")
		}
		values[key.Value] = root.Content[i+1]
	}
	var spec tableSpec
	if n := values["caption"]; n != nil {
		v, e := scalarString(n)
		if e != nil {
			return spec, e
		}
		spec.caption = v
	}
	if n := values["columns"]; n != nil {
		v, e := stringSequence(n)
		if e != nil {
			return spec, e
		}
		spec.columns = v
	}
	if n := values["align"]; n != nil {
		v, e := stringSequenceAllowNull(n)
		if e != nil {
			return spec, e
		}
		for i, a := range v {
			switch a {
			case "", "left", "center", "right":
			default:
				return spec, fmt.Errorf("align entry %d must be left, center, right, or null", i+1)
			}
		}
		spec.align = v
	}
	if n := values["rows"]; n != nil {
		if n.Kind != yaml.SequenceNode {
			return spec, fmt.Errorf("rows must be a sequence")
		}
		for i, row := range n.Content {
			if row.Kind != yaml.SequenceNode {
				return spec, fmt.Errorf("row %d must be a sequence", i+1)
			}
			vals := make([]string, len(row.Content))
			for j, cell := range row.Content {
				v, e := scalarString(cell)
				if e != nil {
					return spec, fmt.Errorf("row %d column %d: %w", i+1, j+1, e)
				}
				vals[j] = v
			}
			spec.rows = append(spec.rows, vals)
		}
	}
	if n := values["header_col"]; n != nil {
		if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" {
			return spec, fmt.Errorf("header_col must be a boolean")
		}
		spec.headerCol = n.Value == "true"
	}
	return spec, nil
}

func scalarString(n *yaml.Node) (string, error) {
	if n == nil {
		return "", nil
	}
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("expected a scalar value")
	}
	if n.Tag == "!!null" {
		return "", nil
	}
	return n.Value, nil
}
func stringSequence(n *yaml.Node) ([]string, error) {
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("expected a sequence")
	}
	out := make([]string, len(n.Content))
	for i, item := range n.Content {
		v, e := scalarString(item)
		if e != nil {
			return nil, fmt.Errorf("entry %d: %w", i+1, e)
		}
		out[i] = v
	}
	return out, nil
}
func stringSequenceAllowNull(n *yaml.Node) ([]string, error) { return stringSequence(n) }
func alignAt(a []string, i int) string {
	if i >= len(a) {
		return ""
	}
	return a[i]
}
func alignAttr(a string) string {
	if a == "" {
		return ""
	}
	return fmt.Sprintf(` data-align="%s"`, a)
}

func highlightCode(language, source string) string {
	lexer := lexers.Get(language)
	if lexer == nil {
		return `<pre class="chroma"><code class="language-` + html.EscapeString(language) + `">` + html.EscapeString(source) + `</code></pre>`
	}
	iterator, err := lexer.Tokenise(nil, source)
	if err != nil {
		return `<pre class="chroma"><code class="language-` + html.EscapeString(language) + `">` + html.EscapeString(source) + `</code></pre>`
	}
	formatter := chromahtml.New(chromahtml.WithClasses(true), chromahtml.WithCSSComments(false))
	var buf bytes.Buffer
	if err := formatter.Format(&buf, styles.Fallback, iterator); err != nil {
		return `<pre class="chroma"><code class="language-` + html.EscapeString(language) + `">` + html.EscapeString(source) + `</code></pre>`
	}
	output := buf.String()
	if !strings.HasPrefix(output, "<pre") {
		output = "<pre class=\"chroma\"><code>" + output + "</code></pre>"
	}
	output = regexp.MustCompile(`^<pre(?: class="[^"]*")?><code>`).ReplaceAllString(output, `<pre class="chroma"><code class="language-`+html.EscapeString(language)+`">`)
	if !strings.Contains(output, `class="chroma"`) {
		output = strings.Replace(output, "<pre>", `<pre class="chroma">`, 1)
	}
	if !strings.Contains(output, `class="language-`) {
		output = strings.Replace(output, "<code>", `<code class="language-`+html.EscapeString(language)+`">`, 1)
	}
	return output
}
