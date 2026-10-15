package llm

import "strings"

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

// ThinkFilter strips <think>...</think> blocks from a token stream.
// Tags may be split across chunks. Leading whitespace of the visible output is dropped.
type ThinkFilter struct {
	pending string
	inThink bool
	emitted bool
}

func (f *ThinkFilter) Write(s string) string {
	f.pending += s
	var out strings.Builder
	for {
		if f.inThink {
			i := strings.Index(f.pending, thinkClose)
			if i < 0 {
				f.pending = f.pending[len(f.pending)-partialSuffixLen(f.pending, thinkClose):]
				return out.String()
			}
			f.pending = f.pending[i+len(thinkClose):]
			f.inThink = false
			continue
		}
		i := strings.Index(f.pending, thinkOpen)
		if i < 0 {
			cut := len(f.pending) - partialSuffixLen(f.pending, thinkOpen)
			f.emit(&out, f.pending[:cut])
			f.pending = f.pending[cut:]
			return out.String()
		}
		f.emit(&out, f.pending[:i])
		f.pending = f.pending[i+len(thinkOpen):]
		f.inThink = true
	}
}

// Flush returns any buffered visible text. An unterminated think block is discarded.
func (f *ThinkFilter) Flush() string {
	if f.inThink {
		f.pending = ""
		return ""
	}
	var out strings.Builder
	f.emit(&out, f.pending)
	f.pending = ""
	return out.String()
}

func (f *ThinkFilter) emit(out *strings.Builder, s string) {
	if !f.emitted {
		s = strings.TrimLeft(s, " \t\r\n")
		if s == "" {
			return
		}
		f.emitted = true
	}
	out.WriteString(s)
}

// partialSuffixLen returns the length of the longest suffix of s that is a proper prefix of tag.
func partialSuffixLen(s, tag string) int {
	for n := min(len(s), len(tag)-1); n > 0; n-- {
		if strings.HasSuffix(s, tag[:n]) {
			return n
		}
	}
	return 0
}
