package welcome

import "github.com/siraj/konsolai/internal/config"

// ResolveLimits fills zero layout cuts from the built-in profile.
// A zero Limits paints the same screen as today's built-in cuts.
func ResolveLimits(l Limits) Limits {
	b := config.Builtin()
	if l.TinyHeight < 1 {
		l.TinyHeight = b.LayoutTinyHeight()
	}
	if l.RecentHeight < 1 {
		l.RecentHeight = b.LayoutRecentHeight()
	}
	if l.TinyWidth < 1 {
		l.TinyWidth = b.LayoutTinyWidth()
	}
	if l.NarrowWidth < 1 {
		l.NarrowWidth = b.LayoutNarrowWidth()
	}
	if l.FrameWidth < 1 {
		l.FrameWidth = b.LayoutFrameWidth()
	}
	return l
}
