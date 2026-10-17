package repl

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/banner"
	"github.com/siraj/konsolai/internal/welcome"
)

func TestAwaitStartupReturnsWhenReady(t *testing.T) {
	data := make(chan welcome.Data, 1)
	data <- welcome.Data{Placeholder: "ship it", Version: "v"}
	bannerDone := make(chan struct{})
	close(bannerDone)
	start := time.Now()
	view, ok := awaitStartup(data, bannerDone)
	if time.Since(start) > 100*time.Millisecond {
		t.Fatalf("waited %s", time.Since(start))
	}
	if !ok || view.Placeholder != "ship it" {
		t.Fatalf("ok=%v %+v", ok, view)
	}
}

func TestAwaitStartupAbandonsSlowGather(t *testing.T) {
	data := make(chan welcome.Data)
	bannerDone := make(chan struct{})
	close(bannerDone)
	start := time.Now()
	view, _ := awaitStartup(data, bannerDone)
	elapsed := time.Since(start)
	if elapsed < 300*time.Millisecond || elapsed > 700*time.Millisecond {
		t.Fatalf("waited %s", elapsed)
	}
	if view.Placeholder != "" || len(view.Models) != 0 {
		t.Fatalf("used a late payload %+v", view)
	}
}

func TestWelcomeRedrawUsesCache(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	prev := gatherData
	gatherData = func(context.Context, welcome.Input) (welcome.Data, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return welcome.Data{Session: welcome.Session{Dir: "FRESH_DIR"}, Placeholder: "fresh"}, nil
	}
	t.Cleanup(func() {
		gatherData = prev
		select {
		case <-release:
		default:
			close(release)
		}
	})
	t.Setenv("COLUMNS", "120")
	t.Setenv("LINES", "40")
	st := &welcomeState{snap: welcomeSnap{
		at: time.Now().Add(-61 * time.Second),
		data: welcome.Data{
			Version:     "v",
			Placeholder: "cached-ph",
			Session:     welcome.Session{Dir: "CACHED_DIR", Branch: "dev", Memory: "memory offline"},
			Models:      []welcome.Model{{Name: "cached-model", Role: "main", Status: welcome.StatusUp, Stat: "ok"}},
			Up:          1,
			Total:       1,
		},
		info:     banner.Info{Version: "v", Model: "cached-model", Dir: "CACHED_DIR", Branch: "dev"},
		layout:   "split",
		wordmark: true,
	}}
	var b bytes.Buffer
	done := make(chan struct{})
	go func() {
		st.redraw(&b)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("redraw blocked on refresh")
	}
	out := b.String()
	if !strings.Contains(out, "CACHED_DIR") || strings.Contains(out, "FRESH_DIR") {
		t.Fatalf("paint %q", out)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
}
