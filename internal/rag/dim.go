package rag

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// MismatchLine is the one process-wide notice when the embed model and the
// memory index disagree. Later calls do not print it again.
func MismatchLine(model, index int) string {
	return fmt.Sprintf("rag disabled: embed model returns %d dims, memory index expects %d; use a %d-dim model or run /index clear and /set rag reset-dim", model, index, index)
}

type dimCatalog interface {
	EmbeddingDim(ctx context.Context) (int, error)
}

type dimState struct {
	mu       sync.Mutex
	seen     bool
	disabled bool
	printed  bool
	model    int
	index    int
	notice   io.Writer
}

var dims dimState

// ResetDims forgets the process decision so the next embedding is checked again.
func ResetDims() {
	dims.mu.Lock()
	dims.seen = false
	dims.disabled = false
	dims.printed = false
	dims.model = 0
	dims.index = 0
	dims.mu.Unlock()
}

// Snapshot reports the decision from the first embedding, when one exists.
func Snapshot() (model, index int, decided, disabled bool) {
	dims.mu.Lock()
	defer dims.mu.Unlock()
	return dims.model, dims.index, dims.seen, dims.disabled
}

// Note records a measured pair without printing. The first RAG use prints
// the mismatch line once if the lengths differ.
func Note(model, index int) {
	if model <= 0 || index <= 0 {
		return
	}
	dims.mu.Lock()
	defer dims.mu.Unlock()
	if dims.seen {
		return
	}
	dims.seen = true
	dims.model = model
	dims.index = index
	dims.disabled = model != index
}

func chunkCatalog(st ChunkStore) dimCatalog {
	cat, _ := st.(dimCatalog)
	return cat
}

func searchCatalog(st SearchBackend) dimCatalog {
	cat, _ := st.(dimCatalog)
	return cat
}

func indexCatalog(st IndexBackend) dimCatalog {
	cat, _ := st.(dimCatalog)
	return cat
}

func setProcessNotice(w io.Writer) {
	dims.mu.Lock()
	dims.notice = w
	dims.mu.Unlock()
}

func ragBlocked(notice io.Writer) bool {
	dims.mu.Lock()
	defer dims.mu.Unlock()
	if !dims.disabled {
		return false
	}
	announceLocked(notice)
	return true
}

// acceptDims is true when a vector of length n may be written or searched.
// A nil catalog means the store cannot report a width, so the call is allowed.
func acceptDims(ctx context.Context, cat dimCatalog, n int, notice io.Writer) bool {
	if n <= 0 {
		return false
	}
	if cat == nil {
		return true
	}
	dims.mu.Lock()
	if dims.seen {
		announceLocked(notice)
		allow := !dims.disabled
		dims.mu.Unlock()
		return allow
	}
	dims.mu.Unlock()

	m, err := cat.EmbeddingDim(ctx)
	if err != nil || m <= 0 {
		return false
	}

	dims.mu.Lock()
	defer dims.mu.Unlock()
	if dims.seen {
		announceLocked(notice)
		return !dims.disabled
	}
	dims.seen = true
	dims.model = n
	dims.index = m
	if n != m {
		dims.disabled = true
		announceLocked(notice)
		return false
	}
	return true
}

func announceLocked(notice io.Writer) {
	if !dims.disabled || dims.printed {
		return
	}
	w := notice
	if w == nil {
		w = dims.notice
	}
	if w == nil {
		return
	}
	dims.printed = true
	fmt.Fprintln(w, MismatchLine(dims.model, dims.index))
}
