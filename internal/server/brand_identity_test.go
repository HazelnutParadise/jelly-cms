package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	nethtml "golang.org/x/net/html"
)

const brandIdentitySHA256 = "a7b81e6a34f0f354b4c3c788f59a511d43e330e9eb2a8f1bbc3a68265682b32a"

func brandIdentityAttr(n *nethtml.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func brandIdentityHasClass(n *nethtml.Node, class string) bool {
	for _, got := range strings.Fields(brandIdentityAttr(n, "class")) {
		if got == class {
			return true
		}
	}
	return false
}

func brandIdentityFind(t *testing.T, body string, predicate func(*nethtml.Node) bool) []*nethtml.Node {
	t.Helper()
	doc, err := nethtml.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse rendered HTML: %v", err)
	}
	var found []*nethtml.Node
	var walk func(*nethtml.Node)
	walk = func(n *nethtml.Node) {
		if predicate(n) {
			found = append(found, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return found
}

func brandIdentityChildren(t *testing.T, body string, predicate func(*nethtml.Node) bool) []*nethtml.Node {
	t.Helper()
	parents := brandIdentityFind(t, body, predicate)
	var children []*nethtml.Node
	for _, parent := range parents {
		for c := parent.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == nethtml.ElementNode && (c.Data == "img" || brandIdentityHasClass(c, "mark") || c.Data == "svg" || brandIdentityHasClass(c, "cubes")) {
				children = append(children, c)
			}
		}
	}
	return children
}

func brandIdentityAssertImage(t *testing.T, h *accessHarness, page, region string, nodes []*nethtml.Node, count *int) {
	t.Helper()
	if len(nodes) != 1 {
		t.Errorf("%s %s contains %d brand images, want exactly one", page, region, len(nodes))
		return
	}
	(*count)++
	img := nodes[0]
	if img.Data != "img" {
		t.Errorf("%s %s brand is <%s>, want an external SVG image", page, region, img.Data)
		return
	}
	src := brandIdentityAttr(img, "src")
	u, err := url.Parse(src)
	if err != nil || u.IsAbs() || u.Host != "" || u.Path != "/admin/assets/jelly-mark.svg" || len(u.Query()["v"]) != 1 {
		t.Errorf("%s %s image src=%q, want local hash-versioned jelly-mark.svg", page, region, src)
		return
	}
	asset := h.request(http.MethodGet, src, "", "", "", "")
	if asset.code != http.StatusOK {
		t.Errorf("GET %s brand asset status=%d, want 200", src, asset.code)
		return
	}
	digest := sha256.Sum256([]byte(asset.body))
	got := hex.EncodeToString(digest[:])
	if got != brandIdentitySHA256 || u.Query().Get("v") != got {
		t.Errorf("%s %s SVG/hash=%s/%s, want canonical %s", page, region, got, u.Query().Get("v"), brandIdentitySHA256)
	}
}

func TestBrandIdentityUsesCanonicalSVGInRenderedHTTPPages(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "brand-identity-admin")
	adminPage := h.request(http.MethodGet, "/admin/posts", "", h.token(admin), echo.MIMETextHTML, "")
	if adminPage.code != http.StatusOK {
		t.Fatalf("GET /admin/posts status=%d, want 200", adminPage.code)
	}
	public := archivesPage(t, h, "/")

	identitySlots := 0
	brandIdentityAssertImage(t, h, "/admin/posts", "sidebar", brandIdentityChildren(t, adminPage.body, func(n *nethtml.Node) bool {
		return n.Type == nethtml.ElementNode && n.Data == "a" && brandIdentityHasClass(n, "sidebar-logo")
	}), &identitySlots)
	brandIdentityAssertImage(t, h, "/admin/posts", "palette footer", brandIdentityChildren(t, adminPage.body, func(n *nethtml.Node) bool {
		return n.Type == nethtml.ElementNode && n.Data == "span" && brandIdentityHasClass(n, "brand") && n.Parent != nil && brandIdentityHasClass(n.Parent, "palette-foot")
	}), &identitySlots)
	brandIdentityAssertImage(t, h, "/", "public header", brandIdentityChildren(t, public, func(n *nethtml.Node) bool {
		return n.Type == nethtml.ElementNode && n.Data == "a" && brandIdentityHasClass(n, "brand") && n.Parent != nil && n.Parent.Data == "div" && n.Parent.Parent != nil && n.Parent.Parent.Data == "header"
	}), &identitySlots)
	brandIdentityAssertImage(t, h, "/", "public footer", brandIdentityChildren(t, public, func(n *nethtml.Node) bool {
		return n.Type == nethtml.ElementNode && n.Data == "a" && brandIdentityHasClass(n, "brand") && n.Parent != nil && n.Parent.Parent != nil && n.Parent.Parent.Data == "div" && n.Parent.Parent.Parent != nil && n.Parent.Parent.Parent.Data == "footer"
	}), &identitySlots)
	if identitySlots != 4 {
		t.Errorf("checked %d rendered identity slots, want all four", identitySlots)
	}
	for label, body := range map[string]string{"admin": adminPage.body, "public": public} {
		for _, node := range brandIdentityFind(t, body, func(n *nethtml.Node) bool {
			return n.Type == nethtml.ElementNode && brandIdentityHasClass(n, "brand")
		}) {
			if brandIdentityHasClass(node, "brand") {
				var walk func(*nethtml.Node)
				walk = func(n *nethtml.Node) {
					if n.Type == nethtml.ElementNode && (n.Data == "svg" || brandIdentityHasClass(n, "cubes")) {
						t.Errorf("%s brand identity still contains inline SVG or CSS cubes", label)
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						walk(c)
					}
				}
				walk(node)
			}
		}
	}
}

func TestBrandIdentityECPayEmbeddedSVGMatchesCanonicalShapes(t *testing.T) {
	canonical, err := os.ReadFile(filepath.Join("..", "..", "web", "admin", "assets", "jelly-mark.svg"))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(canonical)
	if got := hex.EncodeToString(hash[:]); got != brandIdentitySHA256 {
		t.Fatalf("canonical SVG hash=%s, want %s", got, brandIdentitySHA256)
	}
	body, err := renderECPayPostForm("https://payment-stage.ecpay.com.tw/Cashier/AioCheckOut/V5", map[string]string{"MerchantID": "fixture", "CheckMacValue": "fixture"}, ecpayHandoffView{
		Language: "zh-TW", SiteTitle: "Fixture", OrderNumber: "TEST-1", Amount: "NT$1", Method: "Credit card", BackURL: "/orders/TEST-1",
	})
	if err != nil {
		t.Fatalf("renderECPayPostForm: %v", err)
	}
	canonicalDoc, err := nethtml.Parse(bytes.NewReader(canonical))
	if err != nil {
		t.Fatalf("parse canonical SVG: %v", err)
	}
	pageDoc, err := nethtml.Parse(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("parse rendered ECPay HTML: %v", err)
	}
	findSVG := func(root *nethtml.Node, class string) *nethtml.Node {
		var found *nethtml.Node
		var walk func(*nethtml.Node)
		walk = func(n *nethtml.Node) {
			if n.Type == nethtml.ElementNode && n.Data == "svg" && (class == "" || brandIdentityHasClass(n, class)) {
				found = n
			}
			for c := n.FirstChild; c != nil && found == nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(root)
		return found
	}
	wantSVG, gotSVG := findSVG(canonicalDoc, ""), findSVG(pageDoc, "mark")
	if wantSVG == nil || gotSVG == nil {
		t.Fatal("canonical or embedded ECPay logo SVG is missing")
	}
	if brandIdentityAttr(gotSVG, "viewBox") != brandIdentityAttr(wantSVG, "viewBox") {
		t.Errorf("ECPay SVG viewBox=%q, canonical=%q", brandIdentityAttr(gotSVG, "viewBox"), brandIdentityAttr(wantSVG, "viewBox"))
	}
	if !brandIdentitySVGChildrenEqual(wantSVG, gotSVG) {
		t.Errorf("ECPay SVG child geometry or attributes differ from canonical logo")
	}
	if strings.Contains(string(body), "<img") || strings.Contains(strings.ToLower(string(body)), "data:") {
		t.Errorf("ECPay handoff added image/data resource markup despite its image-blocking CSP")
	}
}

func brandIdentitySVGChildrenEqual(a, b *nethtml.Node) bool {
	var normalize func(*nethtml.Node, bool) string
	normalize = func(n *nethtml.Node, root bool) string {
		if n.Type != nethtml.ElementNode && n.Type != nethtml.TextNode {
			return ""
		}
		if n.Type == nethtml.TextNode {
			return strings.TrimSpace(n.Data)
		}
		attrs := make([]string, 0, len(n.Attr))
		for _, attr := range n.Attr {
			if root && (attr.Key == "class" || strings.HasPrefix(attr.Key, "aria-")) {
				continue
			}
			attrs = append(attrs, attr.Key+"="+attr.Val)
		}
		// Attribute order is immaterial in SVG source.
		for i := 0; i < len(attrs); i++ {
			for j := i + 1; j < len(attrs); j++ {
				if attrs[j] < attrs[i] {
					attrs[i], attrs[j] = attrs[j], attrs[i]
				}
			}
		}
		var children []string
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if child := normalize(c, false); child != "" {
				children = append(children, child)
			}
		}
		return fmt.Sprintf("<%s %s>%s</%s>", n.Data, strings.Join(attrs, " "), strings.Join(children, ""), n.Data)
	}
	return normalize(a, true) == normalize(b, true)
}
