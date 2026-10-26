package markup

import "errors"

// ErrNoEngine means the server has no math engine. The renderer then emits the
// escaped TeX inside the jc-math element, and the front end renders it with
// KaTeX auto-render instead.
var ErrNoEngine = errors.New("markup: no math engine available")

// RenderMath is the server-side renderer seam. This build deliberately has no
// in-process JavaScript engine: the checked QuickJS binding cannot safely
// honor request cancellation, so evaluating KaTeX here could outlive the
// caller's render deadline. Return ErrNoEngine and let the existing front-end
// KaTeX auto-render path handle the unchanged jc-math source.
func RenderMath(tex string, display bool) (string, error) {
	_ = tex
	_ = display
	return "", ErrNoEngine
}
