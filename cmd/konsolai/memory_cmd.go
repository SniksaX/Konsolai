package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/siraj/konsolai/internal/memory"
	"github.com/siraj/konsolai/internal/repo"
)

func runHistory(ctx context.Context, mem *memory.Guard, profile string) int {
	s := mem.Store()
	if s == nil {
		fmt.Fprintln(os.Stderr, noMemory)
		return 1
	}
	sessions, err := s.ListSessions(ctx, profile, 20)
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	for _, sess := range sessions {
		t := sess.LastActivity.Local().Format("2006-01-02 15:04")
		fmt.Printf("%s  %s  %s  %s  %s\n", t, sess.Mode, sess.Status, sess.Title, sess.CWD)
	}
	return 0
}

func runFacts(ctx context.Context, mem *memory.Guard, profile string, projectID *int64) int {
	s := mem.Store()
	if s == nil {
		fmt.Fprintln(os.Stderr, noMemory)
		return 1
	}
	user, proj, err := s.ListFacts(ctx, profile, projectID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	if len(user) == 0 && len(proj) == 0 {
		fmt.Println("no facts")
		return 0
	}
	if len(user) > 0 {
		fmt.Println("User facts:")
		for _, f := range user {
			fmt.Printf("  [%d] %s\n", f.ID, f.Fact)
		}
	}
	if len(proj) > 0 {
		fmt.Println("Project facts:")
		for _, f := range proj {
			fmt.Printf("  [%d] %s\n", f.ID, f.Fact)
		}
	}
	return 0
}

func runRemember(ctx context.Context, mem *memory.Guard, profile string, projectID *int64, fact, scope string) int {
	s := mem.Store()
	if s == nil {
		fmt.Fprintln(os.Stderr, noMemory)
		return 1
	}
	var err error
	if scope == "project" {
		if projectID == nil {
			fmt.Fprintln(os.Stderr, "konsolai: no git project here")
			return 1
		}
		_, err = s.AddProjectFact(ctx, *projectID, fact, nil)
	} else {
		_, err = s.AddUserFact(ctx, profile, fact, "explicit")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	fmt.Println("remembered")
	return 0
}

func runForget(ctx context.Context, mem *memory.Guard, scope string, id int64) int {
	s := mem.Store()
	if s == nil {
		fmt.Fprintln(os.Stderr, noMemory)
		return 1
	}
	if err := s.DeleteFact(ctx, scope, id); err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	fmt.Println("forgotten")
	return 0
}

func runPrune(ctx context.Context, mem *memory.Guard, retentionDays int) int {
	s := mem.Store()
	if s == nil {
		fmt.Fprintln(os.Stderr, noMemory)
		return 1
	}
	if err := s.PruneIfDue(ctx, retentionDays); err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	fmt.Println("pruned")
	return 0
}

func runUndo(ctx context.Context, mem *memory.Guard, projectID *int64, cwd string, yes bool) int {
	s := mem.Store()
	if s == nil {
		fmt.Fprintln(os.Stderr, noMemory)
		return 1
	}
	rows, err := s.UndoCandidates(ctx, projectID, cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	if len(rows) == 0 {
		fmt.Println("nothing to undo")
		return 0
	}
	for _, r := range rows {
		fmt.Println(r.Path)
		if r.Diff != "" {
			fmt.Println(r.Diff)
		}
	}
	if !yes {
		ok, err := confirmUndo()
		if err != nil {
			fmt.Fprintln(os.Stderr, "konsolai:", err)
			return 1
		}
		if !ok {
			return 3
		}
	}
	base := cwd
	if projectID != nil {
		base = repo.Root(cwd)
	}
	for _, r := range rows {
		if r.UndoBlocked != "" {
			fmt.Printf("%s: skipped (%s)\n", r.Path, r.UndoBlocked)
			continue
		}
		full := filepath.Join(base, r.Path)
		if !r.ExistedBefore {
			cur, err := os.ReadFile(full)
			if err != nil {
				fmt.Printf("%s: %v\n", r.Path, err)
				continue
			}
			if sha256Hex(cur) != r.AfterHash {
				fmt.Printf("%s: modified since, skipped\n", r.Path)
				continue
			}
			if err := os.Remove(full); err != nil {
				fmt.Printf("%s: %v\n", r.Path, err)
				continue
			}
		} else {
			cur, err := os.ReadFile(full)
			if err != nil {
				fmt.Printf("%s: %v\n", r.Path, err)
				continue
			}
			if sha256Hex(cur) != r.AfterHash {
				fmt.Printf("%s: modified since, skipped\n", r.Path)
				continue
			}
			if err := atomicWriteBytes(full, r.BeforeContent); err != nil {
				fmt.Printf("%s: %v\n", r.Path, err)
				continue
			}
		}
		if err := s.MarkReverted(ctx, r.ID); err != nil {
			fmt.Fprintln(os.Stderr, "konsolai:", err)
			return 1
		}
	}
	fmt.Println("undo covers file edits only; shell command effects are not reverted")
	return 0
}

func confirmUndo() (bool, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	fmt.Fprint(f, "undo these files? [y/n] ")
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return false, sc.Err()
	}
	line := strings.ToLower(strings.TrimSpace(sc.Text()))
	return line == "y" || line == "yes", nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func atomicWriteBytes(path string, content []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".konsolai-undo-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := true
	defer func() {
		_ = f.Close()
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(content); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		_ = f.Chmod(fi.Mode().Perm())
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
