package safety

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Guard struct {
	root  string
	base  string
	extra []string
}

func New(root string) (*Guard, error) {
	return NewAt(root, root)
}

// NewAt confines paths to root (the repo root, or cwd when there is no repo)
// and resolves relative paths against base (the invocation cwd).
func NewAt(root, base string) (*Guard, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("safety: abs: %w", err)
	}
	eval, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("safety: eval root: %w", err)
	}
	babs, err := filepath.Abs(base)
	if err != nil {
		return nil, fmt.Errorf("safety: abs base: %w", err)
	}
	beval, err := filepath.EvalSymlinks(babs)
	if err != nil {
		return nil, fmt.Errorf("safety: eval base: %w", err)
	}
	if !containsPath(eval, beval) {
		return nil, fmt.Errorf("safety: base %s is outside root %s", beval, eval)
	}
	return &Guard{root: eval, base: beval}, nil
}

func (g *Guard) Root() string {
	return g.root
}

func (g *Guard) Base() string {
	return g.base
}

// SetBase moves the path-resolution cwd. The new base must stay inside Root.
func (g *Guard) SetBase(base string) error {
	if g == nil {
		return fmt.Errorf("safety: nil guard")
	}
	babs, err := filepath.Abs(base)
	if err != nil {
		return fmt.Errorf("safety: abs base: %w", err)
	}
	beval, err := filepath.EvalSymlinks(babs)
	if err != nil {
		return fmt.Errorf("safety: eval base: %w", err)
	}
	if !containsPath(g.root, beval) {
		return fmt.Errorf("safety: base %s is outside root %s", beval, g.root)
	}
	g.base = beval
	return nil
}

type DeniedError struct {
	Rule   string
	Detail string
}

func (e *DeniedError) Error() string {
	return fmt.Sprintf("denied by rule %s: %s", e.Rule, e.Detail)
}

type rule struct {
	name string
	re   *regexp.Regexp
}

var commandRules = []rule{
	{name: "rm-root", re: reRMRoot},
	{name: "mkfs", re: reMkfs},
	{name: "wipefs", re: reWipefs},
	{name: "dd-dev", re: reDDDev},
	{name: "redir-dev", re: reRedirDev},
	{name: "chmod-root", re: reChmodRoot},
	{name: "forkbomb", re: reForkBomb},
	{name: "pipe-shell", re: rePipeShell},
	{name: "privilege", re: rePrivilege},
	{name: "power", re: rePower},
	{name: "systemctl-power", re: reSystemctlPower},
}

var (
	reRMRoot         = regexp.MustCompile("(?i)(?:^|[\n;|&])\\s*rm\\s+(?:-[a-zA-Z0-9]*\\s+)*-[a-zA-Z0-9]*[rRfF][a-zA-Z0-9]*(?:\\s+-[a-zA-Z0-9]*)*\\s+(?:/\\*|~(?:/)?|\\$\\{HOME\\}|\\$HOME|/[\\w.-]+/?|/)(?:\\s|;|&|\\||$)")
	reMkfs           = regexp.MustCompile("(?i)(?:^|[\n;|&])\\s*mkfs")
	reWipefs         = regexp.MustCompile("(?i)(?:^|[\n;|&])\\s*wipefs\\b")
	reDDDev          = regexp.MustCompile("(?i)(?:^|[\n;|&])\\s*dd\\b[^\\n]*\\bof=/dev/")
	reRedirDev       = regexp.MustCompile("(?i)>{1,2}\\s*/dev/(?:sd|nvme|mmcblk)")
	reChmodRoot      = regexp.MustCompile("(?i)(?:^|[\n;|&])\\s*(?:chmod|chown)\\s+(?:-[a-zA-Z0-9]*\\s+)*-R[a-zA-Z0-9]*\\s+\\S+\\s+(?:~(?:/)?|\\$\\{HOME\\}|\\$HOME|/)(?:\\s|;|&|\\||$)")
	reForkBomb       = regexp.MustCompile(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`)
	rePipeShell      = regexp.MustCompile("(?i)\\b(?:curl|wget)\\b[^\\n|]*\\|\\s*(?:sudo\\s+)?(?:sh|bash|zsh|sudo|python)")
	rePrivilege      = regexp.MustCompile("(?i)(?:^|[\n;|&])\\s*(?:sudo|doas|su|pkexec)\\b")
	rePower          = regexp.MustCompile("(?i)(?:^|[\n;|&])\\s*(?:shutdown|reboot|poweroff|halt)\\b")
	reSystemctlPower = regexp.MustCompile("(?i)(?:^|[\n;|&])\\s*systemctl\\s+(?:poweroff|reboot|halt)\\b")
)

func (g *Guard) CheckCommand(cmd string) error {
	for _, r := range commandRules {
		if r.re.MatchString(cmd) {
			return &DeniedError{Rule: r.name, Detail: cmd}
		}
	}
	return nil
}

func (g *Guard) ResolvePath(p string) (string, error) {
	return g.resolve(p, g.base)
}

// ResolveFromRoot joins a relative path to Root (for FileLister paths).
func (g *Guard) ResolveFromRoot(p string) (string, error) {
	return g.resolve(p, g.root)
}

func (g *Guard) resolve(p, joinRoot string) (string, error) {
	raw := p
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", &DeniedError{Rule: "tilde", Detail: raw}
		}
		exp, ok := expandHome(p, home)
		if !ok {
			return "", &DeniedError{Rule: "tilde", Detail: raw}
		}
		p = exp
	}
	if p == "" {
		p = "."
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(joinRoot, p))
	}
	if !g.allowedAbs(abs) {
		rule := "escape"
		if strings.HasPrefix(raw, "~") {
			rule = "tilde"
		}
		return "", &DeniedError{Rule: rule, Detail: raw}
	}
	resolved, err := evalDeepest(abs)
	if err != nil {
		return "", fmt.Errorf("safety: eval path: %w", err)
	}
	if !g.allowedAbs(resolved) {
		return "", &DeniedError{Rule: "symlink-escape", Detail: raw}
	}
	return resolved, nil
}

// Display formats a root-relative path: relative to Base when inside it,
// otherwise "<root>/"+rel.
func (g *Guard) Display(rootRel string) string {
	abs := filepath.Join(g.root, filepath.FromSlash(rootRel))
	if rel, err := filepath.Rel(g.base, abs); err == nil && containsPath(g.base, abs) {
		return filepath.ToSlash(rel)
	}
	return "<root>/" + filepath.ToSlash(rootRel)
}

func evalDeepest(p string) (string, error) {
	cur := p
	for {
		if _, err := os.Lstat(cur); err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			rest, err := filepath.Rel(cur, p)
			if err != nil {
				return "", err
			}
			if rest == "." {
				return resolved, nil
			}
			return filepath.Join(resolved, rest), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("safety: no existing ancestor for %s", p)
		}
		cur = parent
	}
}

func containsPath(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}
