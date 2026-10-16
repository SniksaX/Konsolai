package sysctx

import "testing"

func TestPkgInfoArchYay(t *testing.T) {
	p := PkgInfo{
		Family:  "Arch",
		System:  "pacman",
		AUR:     "yay",
		Present: []string{"pacman", "yay", "dpkg"},
	}
	got := p.Line()
	want := "Package manager: pacman (Arch family); AUR helper: yay. dpkg/apt are NOT the system package manager."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPkgInfoDebian(t *testing.T) {
	p := PkgInfo{Family: "Debian", System: "apt", Present: []string{"apt", "dpkg"}}
	got := p.Line()
	if got != "Package manager: apt (Debian family)." {
		t.Fatalf("%q", got)
	}
}

func TestDistroFamily(t *testing.T) {
	f, s := distroFamily("arch", "")
	if f != "Arch" || s != "pacman" {
		t.Fatalf("%s %s", f, s)
	}
	f, s = distroFamily("ubuntu", "debian")
	if f != "Debian" || s != "apt" {
		t.Fatalf("%s %s", f, s)
	}
}

func TestDetectPkgSmoke(t *testing.T) {
	p := detectPkg()
	if p.Line() == "" && len(p.Present) == 0 {
		t.Fatal("expected some package manager on this host")
	}
}
