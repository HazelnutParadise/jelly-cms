package markup

import "errors"

// ErrNoEngine means the server has no math engine. The renderer then emits the
// escaped TeX inside the jc-math element, and the front end renders it with
// KaTeX auto-render instead.
var ErrNoEngine = errors.New("markup: no math engine available")

// RenderMath renders one TeX expression to KaTeX markup on the server (run by
// QuickJS). display selects the block form. The output is placed inside the
// jc-math element that Render emits, and it is sanitized by the same policy.
//
// Contract: the server engine is verified in G0. If the QuickJS check fails,
// the front-end auto-render path replaces it and this function keeps returning
// ErrNoEngine. The jc-math markup does not change.
//
// Stub: always returns ErrNoEngine.
func RenderMath(tex string, display bool) (string, error) {
	_ = tex
	_ = display
	return "", ErrNoEngine
}
