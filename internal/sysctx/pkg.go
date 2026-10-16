package sysctx

import (
	"os"
	"os/exec"
	"strings"
)

var pkgBins = []string{
	"pacman", "yay", "paru", "apt", "dpkg", "dnf", "rpm", "zypper", "apk",
	"flatpak", "snap", "brew",
}

type PkgInfo struct {
	Family  string
	System  string
	AUR     string
	Present []string
}

func detectPkg() PkgInfo {
	id, like := osReleaseIDs()
	info := PkgInfo{}
	info.Family, info.System = distroFamily(id, like)
	seen := map[string]bool{}
	for _, b := range pkgBins {
		if _, err := exec.LookPath(b); err != nil {
			continue
		}
		if seen[b] {
			continue
		}
		seen[b] = true
		info.Present = append(info.Present, b)
	}
	if info.Family == "Arch" {
		if seen["yay"] {
			info.AUR = "yay"
		} else if seen["paru"] {
			info.AUR = "paru"
		}
	}
	if info.System == "" {
		info.System = inferSystem(seen)
		if info.System == "pacman" {
			info.Family = "Arch"
		}
	}
	return info
}

func osReleaseIDs() (id, like string) {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "ID="); ok {
			id = strings.Trim(v, `"`)
		}
		if v, ok := strings.CutPrefix(line, "ID_LIKE="); ok {
			like = strings.Trim(v, `"`)
		}
	}
	return id, like
}

func distroFamily(id, like string) (family, system string) {
	blob := strings.ToLower(id + " " + like)
	switch {
	case hasToken(blob, "arch"):
		return "Arch", "pacman"
	case hasToken(blob, "debian"), hasToken(blob, "ubuntu"):
		return "Debian", "apt"
	case hasToken(blob, "fedora"), hasToken(blob, "rhel"), hasToken(blob, "centos"):
		return "Fedora", "dnf"
	case hasToken(blob, "suse"):
		return "SUSE", "zypper"
	case hasToken(blob, "alpine"):
		return "Alpine", "apk"
	default:
		return "", ""
	}
}

func hasToken(blob, tok string) bool {
	for _, f := range strings.Fields(blob) {
		if f == tok {
			return true
		}
	}
	return strings.Contains(blob, tok)
}

func inferSystem(seen map[string]bool) string {
	for _, b := range []string{"pacman", "apt", "dnf", "zypper", "apk", "brew"} {
		if seen[b] {
			return b
		}
	}
	if seen["dpkg"] {
		return "apt"
	}
	if seen["rpm"] {
		return "dnf"
	}
	return ""
}

func (p PkgInfo) Line() string {
	if p.System == "" && len(p.Present) == 0 {
		return ""
	}
	var b strings.Builder
	if p.System != "" {
		b.WriteString("Package manager: ")
		b.WriteString(p.System)
		if p.Family != "" {
			b.WriteString(" (")
			b.WriteString(p.Family)
			b.WriteString(" family)")
		}
		if p.AUR != "" {
			b.WriteString("; AUR helper: ")
			b.WriteString(p.AUR)
		}
		b.WriteByte('.')
	}
	if p.notSystemAPT() {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), " ") {
			b.WriteByte(' ')
		}
		b.WriteString("dpkg/apt are NOT the system package manager.")
	}
	return strings.TrimSpace(b.String())
}

func (p PkgInfo) notSystemAPT() bool {
	if p.System == "apt" {
		return false
	}
	for _, b := range p.Present {
		if b == "apt" || b == "dpkg" {
			return true
		}
	}
	return false
}
