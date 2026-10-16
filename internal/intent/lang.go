package intent

import (
	"strings"
	"unicode"
)

var (
	enStop = map[string]bool{
		"the": true, "and": true, "you": true, "are": true, "how": true, "what": true,
		"this": true, "that": true, "with": true, "for": true, "have": true, "was": true,
		"not": true, "your": true, "please": true, "thanks": true, "hello": true, "from": true,
		"they": true, "will": true, "can": true, "just": true, "about": true, "would": true,
		"could": true, "should": true, "like": true, "want": true, "need": true, "help": true,
		"here": true, "there": true, "when": true, "where": true, "which": true, "because": true,
		"into": true, "did": true, "does": true, "been": true, "were": true, "them": true,
	}
	frStop = map[string]bool{
		"les": true, "une": true, "des": true, "est": true, "sont": true, "vous": true,
		"nous": true, "que": true, "qui": true, "pas": true, "pour": true, "dans": true,
		"avec": true, "salut": true, "merci": true, "oui": true, "non": true, "bonjour": true,
		"bonsoir": true, "comment": true, "faire": true, "cette": true, "tout": true,
		"plus": true, "mais": true, "comme": true, "alors": true, "très": true, "bien": true,
		"ça": true, "va": true, "moi": true, "toi": true, "elle": true, "aussi": true,
		"après": true, "avant": true, "encore": true, "quoi": true, "suis": true,
	}
)

func allowedLang(code string, langs []string) bool {
	code = strings.ToLower(strings.TrimSpace(code))
	for _, l := range langs {
		if l == code {
			return true
		}
	}
	return false
}

func normalizeLangs(langs []string, def string) ([]string, string) {
	out := make([]string, 0, len(langs))
	seen := map[string]bool{}
	for _, l := range langs {
		l = strings.ToLower(strings.TrimSpace(l))
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	if len(out) == 0 {
		out = []string{"en", "fr"}
	}
	def = strings.ToLower(strings.TrimSpace(def))
	if def == "" || !allowedLang(def, out) {
		def = "en"
		if !allowedLang(def, out) {
			def = out[0]
		}
	}
	return out, def
}

func letterCount(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

func words(s string) []string {
	s = strings.ToLower(s)
	var out []string
	var cur strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) {
			cur.WriteRune(r)
			continue
		}
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func frenchCharScore(s string) int {
	n := 0
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'à', 'â', 'ä', 'é', 'è', 'ê', 'ë', 'ï', 'î', 'ô', 'ù', 'û', 'ü', 'ÿ', 'ç', 'œ', 'æ':
			n++
		}
	}
	return n
}

// Detect scores en/fr with stopwords and French-character heuristics.
// It returns "" when there is no evidence among langs.
func Detect(text string, langs []string) string {
	langs, _ = normalizeLangs(langs, "")
	en, fr := 0, 0
	for _, w := range words(text) {
		if enStop[w] {
			en++
		}
		if frStop[w] {
			fr++
		}
	}
	fr += 2 * frenchCharScore(text)
	type sc struct {
		lang string
		n    int
	}
	best := sc{}
	tie := false
	for _, l := range langs {
		n := 0
		switch l {
		case "en":
			n = en
		case "fr":
			n = fr
		}
		if n == 0 {
			continue
		}
		if n > best.n {
			best = sc{l, n}
			tie = false
		} else if n == best.n && l != best.lang {
			tie = true
		}
	}
	if best.n == 0 || tie {
		return ""
	}
	return best.lang
}

func LanguageName(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "fr":
		return "French"
	default:
		return "English"
	}
}

// ResolveLanguage picks a profile language for the current user text.
// The router hint is accepted only when it is in langs.
func ResolveLanguage(text, prevUser, routerHint, defaultLang string, langs []string) string {
	langs, defaultLang = normalizeLangs(langs, defaultLang)
	// Enough letters, or a short line with stopword/character evidence.
	if d := Detect(text, langs); d != "" {
		return d
	}
	if prevUser != "" {
		if prev := Detect(prevUser, langs); prev != "" {
			return prev
		}
	}
	hint := strings.ToLower(strings.TrimSpace(routerHint))
	if allowedLang(hint, langs) {
		return hint
	}
	return defaultLang
}
