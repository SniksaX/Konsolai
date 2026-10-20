package intent

import (
	"path/filepath"
	"strings"
	"unicode"
)

var defaultVerbs = []string{
	"create", "write", "make", "add", "generate", "fix", "update", "edit",
	"change", "refactor", "rename", "delete", "remove", "install", "run",
	"build", "deploy", "commit", "move", "copy",
	"kill", "stop", "start", "restart", "test", "free", "close", "open", "launch",
	"crée", "créer", "écris", "écrire", "ajoute", "ajouter", "génère", "générer",
	"corrige", "corriger", "modifie", "modifier", "supprime", "supprimer",
	"installe", "installer", "lance", "lancer", "déplace", "renomme",
	"tue", "tuer", "arrête", "arrete", "arrêter", "arreter", "démarre", "demarre",
	"relance", "teste", "libère", "libere", "ferme",
}

var defaultTargets = []string{
	"file", "folder", "directory", "project", "repo", "branch", "readme",
	"test", "function", "endpoint", "fichier", "dossier", "projet", "branche",
	"process", "pid", "port", "server", "container", "service",
	"processus", "serveur", "conteneur",
}

var pronounTargets = []string{
	"it", "that", "this", "them", "ça", "ca", "le", "la", "les", "ce",
}

var objectHints = []string{
	"process", "pid", "port", "file", "server", "container", "branch", "service",
	"processus", "fichier", "serveur", "conteneur", "branche", "dossier",
	"readme", "endpoint",
}

var buildTargetWords = map[string]bool{
	"readme": true, "test": true, "function": true, "endpoint": true,
	"docs": true, "doc": true,
}

var systemTargetWords = map[string]bool{
	"process": true, "pid": true, "port": true, "server": true, "container": true,
	"service": true, "processus": true, "serveur": true, "conteneur": true,
}

var containerWords = map[string]bool{
	"file": true, "folder": true, "directory": true, "fichier": true, "dossier": true,
}

var howPrefixes = []string{"how", "comment", "pourquoi", "why"}

func ActionFloor(text string, verbs, targets []string) (Intent, bool) {
	return ActionFloorPrev(text, "", verbs, targets, false)
}

func ActionFloorPrev(text, prev string, verbs, targets []string, inRepo bool) (Intent, bool) {
	if verbs == nil {
		verbs = defaultVerbs
	}
	if targets == nil {
		targets = defaultTargets
	}
	trim := strings.TrimSpace(text)
	if trim == "" {
		return "", false
	}
	folded := fold(trim)
	first := firstWord(folded)
	for _, p := range howPrefixes {
		if first == p {
			return "", false
		}
	}
	if MentionsObject(prev) {
		targets = append(append([]string{}, targets...), pronounTargets...)
	}
	if inRepo && hasTestDeictic(folded) {
		return Build, true
	}
	if !hasWord(folded, verbs) {
		return "", false
	}
	hasFile := hasFilename(trim)
	hasNamed := hasWord(folded, targets)
	if !hasFile && !hasNamed {
		return "", false
	}
	if hasSystemTarget(folded) {
		return Act, true
	}
	if hasFile {
		return Build, true
	}
	if hasBuildWord(folded) && !hasContainer(folded) {
		return Build, true
	}
	if hasBuildWord(folded) && hasWord(folded, []string{"readme", "function", "endpoint", "docs", "doc"}) {
		return Build, true
	}
	if hasWord(folded, []string{"readme", "function", "endpoint"}) {
		return Build, true
	}
	return Act, true
}

func MentionsObject(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if hasFilename(s) {
		return true
	}
	folded := fold(s)
	if hasWord(folded, objectHints) {
		return true
	}
	if strings.Contains(folded, "port ") {
		return true
	}
	return false
}

func hasTestDeictic(folded string) bool {
	if !hasWord(folded, []string{"test", "teste", "tester"}) {
		return false
	}
	return hasWord(folded, pronounTargets)
}

func hasSystemTarget(folded string) bool {
	for _, w := range wordsOf(folded) {
		if systemTargetWords[w] {
			return true
		}
	}
	return false
}

func RaiseTo(d Decision, floor Intent) Decision {
	if floor == "" {
		return d
	}
	if rank(d.Intent) < rank(floor) {
		d.Intent = floor
		if d.Reason == "" {
			d.Reason = "action floor"
		}
	}
	return d
}

func rank(i Intent) int {
	switch i {
	case Build:
		return 3
	case Act:
		return 2
	case Lookup:
		return 1
	default:
		return 0
	}
}

func fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'à', 'á', 'â', 'ä', 'ã':
			b.WriteByte('a')
		case 'è', 'é', 'ê', 'ë':
			b.WriteByte('e')
		case 'ì', 'í', 'î', 'ï':
			b.WriteByte('i')
		case 'ò', 'ó', 'ô', 'ö', 'õ':
			b.WriteByte('o')
		case 'ù', 'ú', 'û', 'ü':
			b.WriteByte('u')
		case 'ç':
			b.WriteByte('c')
		case 'ñ':
			b.WriteByte('n')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	for i, r := range s {
		if !unicode.IsLetter(r) && r != '\'' {
			return s[:i]
		}
	}
	return s
}

func wordsOf(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || r == '_' {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func hasWord(folded string, list []string) bool {
	have := map[string]bool{}
	for _, w := range wordsOf(folded) {
		have[w] = true
	}
	for _, v := range list {
		if have[fold(v)] {
			return true
		}
	}
	return false
}

func hasBuildWord(folded string) bool {
	for _, w := range wordsOf(folded) {
		if buildTargetWords[w] {
			return true
		}
	}
	return false
}

func hasContainer(folded string) bool {
	for _, w := range wordsOf(folded) {
		if containerWords[w] {
			return true
		}
	}
	return false
}

func hasFilename(text string) bool {
	for _, tok := range strings.Fields(text) {
		tok = strings.Trim(tok, ".,;:!?\"'`")
		if tok == "" {
			continue
		}
		base := filepath.Base(tok)
		ext := filepath.Ext(base)
		if ext == "" || ext == base {
			continue
		}
		name := strings.TrimSuffix(base, ext)
		if name == "" {
			continue
		}
		return true
	}
	if strings.Contains(text, "/") || strings.Contains(text, `\`) {
		return true
	}
	return false
}
