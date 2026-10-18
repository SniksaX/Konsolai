package welcome

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/siraj/konsolai/internal/ui"
)

type healthKind int

const (
	healthOnline healthKind = iota
	healthDegraded
	healthOffline
)

func healthOf(d Data) healthKind {
	if len(d.Models) == 0 {
		if d.Total > 0 && d.Up >= d.Total {
			return healthOnline
		}
		if d.Up == 0 {
			return healthOffline
		}
		return healthDegraded
	}
	up, bad := 0, 0
	for _, m := range d.Models {
		switch m.Status {
		case StatusUp:
			up++
		case StatusDown, StatusTimeout:
			bad++
		}
	}
	n := len(d.Models)
	if up == n {
		return healthOnline
	}
	if bad == n {
		return healthOffline
	}
	return healthDegraded
}

func hostName(d Data) string {
	if h := strings.TrimSpace(d.Host); h != "" {
		return h
	}
	return "localhost"
}

func statusStrip(pal ui.Palette, d Data, width int) string {
	var segs []string
	switch healthOf(d) {
	case healthOffline:
		segs = append(segs, pal.Error("○")+" "+pal.Meta(hostName(d))+" "+pal.Error("offline"))
	default:
		dot := pal.Success("●")
		if healthOf(d) == healthDegraded && d.Up == 0 {
			dot = pal.Warn("●")
		}
		segs = append(segs, dot+" "+pal.Meta(fmt.Sprintf("%d/%d", d.Up, d.Total))+" "+pal.Muted("up"))
		if healthOf(d) == healthDegraded {
			for _, m := range d.Models {
				if m.Status == StatusUp {
					continue
				}
				name := modelName(m)
				segs = append(segs, paintDot(pal, m.Status)+" "+pal.Meta(name))
			}
		}
	}
	switch {
	case d.Session.Branch == "not a git repo":
		segs = append(segs, pal.Muted("not a git repo"))
	case branchLabel(d.Session) != "":
		seg := pal.Accent(branchLabel(d.Session))
		if d.Session.Changed > 0 {
			n := strconv.Itoa(d.Session.Changed)
			seg += " " + pal.Meta(n) + " " + pal.Muted("changed")
		}
		segs = append(segs, seg)
	case d.Session.Changed > 0:
		n := strconv.Itoa(d.Session.Changed)
		segs = append(segs, pal.Meta(n)+" "+pal.Muted("changed"))
	}
	if d.Session.Ahead > 0 {
		n := strconv.Itoa(d.Session.Ahead)
		segs = append(segs, pal.Muted("ahead")+" "+pal.Meta(n))
	}
	if d.Session.Behind > 0 {
		n := strconv.Itoa(d.Session.Behind)
		segs = append(segs, pal.Muted("behind")+" "+pal.Meta(n))
	}
	if mem := strings.TrimSpace(d.Session.Memory); mem != "" {
		segs = append(segs, paintMetaErr(pal, mem))
	}
	var b strings.Builder
	for i, seg := range segs {
		if i > 0 {
			b.WriteString(" ")
			b.WriteString(pal.Subtle("·"))
			b.WriteString(" ")
		}
		b.WriteString(seg)
	}
	return clip("  "+b.String(), width)
}

func branchLabel(s Session) string {
	if s.Branch == "" || s.Branch == "not a git repo" {
		return ""
	}
	b := s.Branch
	if s.Dirty && !strings.HasSuffix(b, "*") {
		b += "*"
	}
	return b
}

func paintDot(pal ui.Palette, st Status) string {
	switch st {
	case StatusUp:
		return pal.Success("●")
	case StatusSlow:
		return pal.Warn("●")
	case StatusDown, StatusTimeout:
		return pal.Error("○")
	default:
		return pal.Muted("○")
	}
}
