package theme

import (
	"github.com/alecthomas/chroma/v2"
)

func ChromaStyle() *chroma.Style {
	hex := func(r Role) string {
		c := RoleRGB(r)
		return c.Hex()
	}
	entries := chroma.StyleEntries{
		chroma.Keyword:            hex(Accent),
		chroma.KeywordReserved:    hex(Accent),
		chroma.KeywordType:        hex(Accent),
		chroma.KeywordConstant:    hex(Accent),
		chroma.KeywordDeclaration: hex(Accent),
		chroma.String:             hex(Meta),
		chroma.StringChar:         hex(Meta),
		chroma.Comment:            hex(Muted),
		chroma.CommentSingle:      hex(Muted),
		chroma.CommentMultiline:   hex(Muted),
		chroma.Number:             hex(Heading),
		chroma.NumberInteger:      hex(Heading),
		chroma.NumberFloat:        hex(Heading),
		chroma.Name:               "",
		chroma.Text:               "",
		chroma.Background:         "",
	}
	st, err := chroma.NewStyle("konsolai", entries)
	if err != nil {
		return chroma.MustNewStyle("konsolai-plain", chroma.StyleEntries{})
	}
	return st
}
