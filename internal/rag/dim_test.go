package rag

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeDimCatalog struct {
	dim int
	err error
}

func (f fakeDimCatalog) EmbeddingDim(context.Context) (int, error) { return f.dim, f.err }

func TestAcceptDimsGuardsTheIndexWidth(t *testing.T) {
	ResetDims()
	t.Cleanup(ResetDims)
	ctx := context.Background()
	var notice bytes.Buffer

	if acceptDims(ctx, nil, 0, &notice) {
		t.Fatal("zero width must be rejected")
	}
	if !acceptDims(ctx, nil, 8, &notice) {
		t.Fatal("a nil catalog allows the write")
	}

	if !acceptDims(ctx, fakeDimCatalog{dim: 8}, 8, &notice) {
		t.Fatal("matching width must pass")
	}
	if notice.Len() != 0 {
		t.Fatalf("notice %q", notice.String())
	}

	ResetDims()
	notice.Reset()
	if acceptDims(ctx, fakeDimCatalog{dim: 16}, 8, &notice) {
		t.Fatal("a mismatch must block")
	}
	if !strings.Contains(notice.String(), "rag disabled") {
		t.Fatalf("notice %q", notice.String())
	}
	notice.Reset()
	if acceptDims(ctx, fakeDimCatalog{dim: 16}, 8, &notice) {
		t.Fatal("a blocked process stays blocked")
	}
	if notice.Len() != 0 {
		t.Fatalf("the notice must print once: %q", notice.String())
	}
	if !ragBlocked(nil) {
		t.Fatal("ragBlocked must report the decision")
	}

	ResetDims()
	if acceptDims(ctx, fakeDimCatalog{err: errors.New("down")}, 8, &notice) {
		t.Fatal("a catalog error must block")
	}
}

func TestNoteRecordsTheFirstPairOnly(t *testing.T) {
	ResetDims()
	t.Cleanup(ResetDims)
	Note(4, 4)
	if m, idx, seen, disabled := Snapshot(); m != 4 || idx != 4 || !seen || disabled {
		t.Fatalf("snapshot %d %d %v %v", m, idx, seen, disabled)
	}
	Note(8, 4)
	if m, idx, _, disabled := Snapshot(); m != 4 || idx != 4 || disabled {
		t.Fatalf("later note changed the pair: %d %d %v", m, idx, disabled)
	}

	ResetDims()
	Note(4, 8)
	if _, _, _, disabled := Snapshot(); !disabled {
		t.Fatal("a mismatch disables rag")
	}
}
