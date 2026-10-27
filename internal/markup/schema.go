package markup

func schemaDocument() SchemaDoc {
	blocks := []BlockSpec{
		{Name: "hero", Kind: "container", Attrs: containerSchemaAttrs("height")},
		{Name: "section", Kind: "container", Attrs: containerSchemaAttrs("height")},
		{Name: "columns", Kind: "container", Attrs: containerSchemaAttrs("ratio")},
		{Name: "col", Kind: "container", Attrs: containerSchemaAttrs("")},
		{Name: "sidebar", Kind: "container", Attrs: containerSchemaAttrs("")},
		{Name: "cta", Kind: "container", Attrs: containerSchemaAttrs("")},
		{Name: "gallery", Kind: "container", Attrs: containerSchemaAttrs("")},
		{Name: "carousel", Kind: "container", Attrs: containerSchemaAttrs("")},
		{Name: "faq", Kind: "container", Attrs: containerSchemaAttrs("")},
		{Name: "item", Kind: "container", Attrs: containerSchemaAttrs("title")},
		{Name: "tabs", Kind: "container", Attrs: containerSchemaAttrs("")},
		{Name: "tab", Kind: "container", Attrs: containerSchemaAttrs("title")},
		{Name: "products", Kind: "container", Attrs: dynamicSchemaAttrs()},
		{Name: "posts", Kind: "container", Attrs: dynamicSchemaAttrs()},
		{Name: "embed", Kind: "directive", Attrs: map[string][]string{"url": nil}},
		{Name: "widget", Kind: "directive", Attrs: map[string][]string{"name": nil, "to": nil}},
		{Name: "counter", Kind: "directive", Attrs: map[string][]string{"to": nil}},
		{Name: "countdown", Kind: "directive", Attrs: map[string][]string{"to": nil}},
		{Name: "compare", Kind: "directive", Attrs: map[string][]string{"before": nil, "after": nil}},
	}
	slash := []SlashItem{
		{Key: "h1", Label: "大標題", Insert: "# 標題"},
		{Key: "h2", Label: "標題", Insert: "## 標題"},
		{Key: "quote", Label: "引用", Insert: "> "},
		{Key: "task", Label: "待辦", Insert: "- [ ] 待辦事項"},
		{Key: "code", Label: "程式碼", Insert: "```go\n\n```"},
		{Key: "table", Label: "YAML 表格", Insert: "```table\ncolumns: [欄一, 欄二]\nrows: []\n```"},
		{Key: "math", Label: "數學", Insert: "$x^2$"},
		{Key: "latex", Label: "LaTeX 區塊", Insert: "```latex\n\\frac{a}{b}\n```"},
		{Key: "html", Label: "HTML 區塊", Insert: "```html\n\n```"},
		{Key: "image", Label: "圖片", Insert: "![](/uploads/)"},
		{Key: "embed", Label: "嵌入", Insert: "::embed{url=https://}"},
		{Key: "widget", Label: "互動元件", Insert: "::widget{name=countdown to=2030-01-01}"},
		{Key: "counter", Label: "數字跳動", Insert: "::counter{to=1200}"},
		{Key: "countdown", Label: "倒數", Insert: "::countdown{to=2030-01-01}"},
		{Key: "compare", Label: "前後比較", Insert: "::compare{before=/uploads/before.jpg after=/uploads/after.jpg}"},
		{Key: "columns", Label: "雙欄", Insert: ":::columns\n::: col\n左欄\n:::\n::: col\n右欄\n:::\n:::"},
		{Key: "tabs", Label: "分頁", Insert: ":::tabs\n::: tab {title=標籤}\n內容\n:::\n:::"},
	}
	return SchemaDoc{Blocks: blocks, Slash: slash}
}

func dynamicSchemaAttrs() map[string][]string {
	attrs := containerSchemaAttrs("")
	attrs["category"] = nil
	attrs["limit"] = nil
	return attrs
}

func containerSchemaAttrs(extra string) map[string][]string {
	attrs := map[string][]string{
		"id":      nil,
		"class":   {"btn", "t", "lead", "center", "muted"},
		"bg":      {"primary", "secondary", "accent", "warning", "ink", "raspberry", "blue", "mint", "yellow"},
		"align":   {"left", "center", "right"},
		"cubes":   {"true"},
		"reveal":  {"fade-up", "fade", "slide-left", "slide-right", "zoom"},
		"hover":   {"lift", "glow", "tilt"},
		"scroll":  {"parallax"},
		"delay":   nil,
		"dur":     nil,
		"stagger": nil,
		"once":    {"true"},
	}
	if extra == "height" {
		attrs["height"] = []string{"short", "normal", "tall", "full"}
	}
	if extra == "ratio" {
		attrs["ratio"] = []string{"1:1", "2:1", "1:2", "1:1:1"}
	}
	if extra == "title" {
		attrs["title"] = nil
	}
	return attrs
}
