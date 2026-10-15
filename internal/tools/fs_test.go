package tools

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/safety"
)

func fsRegistry(t *testing.T) (*safety.Guard, *Registry, string) {
	t.Helper()
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return g, NewRegistry(g, os.Stdout, nil), root
}

func TestWriteFileAtomicAndMode(t *testing.T) {
	_, reg, root := fsRegistry(t)
	path := filepath.Join(root, "f.txt")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	tool, _ := reg.Get("write_file")
	act, err := tool.Prepare(`{"path":"f.txt","content":"new"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatal(res.Output)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "new" {
		t.Fatalf("got %q", b)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("mode %o", fi.Mode().Perm())
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".konsolai-write-") {
			t.Fatalf("leftover temp %s", e.Name())
		}
	}
}

func TestReadFileBinaryRejected(t *testing.T) {
	_, reg, root := fsRegistry(t)
	if err := os.WriteFile(filepath.Join(root, "b.bin"), []byte{0xff, 0xfe, 0x00, 0x01}, 0644); err != nil {
		t.Fatal(err)
	}
	tool, _ := reg.Get("read_file")
	act, err := tool.Prepare(`{"path":"b.bin"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Output, "binary") {
		t.Fatalf("%+v", res)
	}
}

func TestReadFileTruncationNote(t *testing.T) {
	_, reg, root := fsRegistry(t)
	data := bytes.Repeat([]byte("a"), readFileCap+50)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), data, 0644); err != nil {
		t.Fatal(err)
	}
	tool, _ := reg.Get("read_file")
	act, err := tool.Prepare(`{"path":"big.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatal(res.Output)
	}
	if !strings.Contains(res.Output, "truncated") {
		t.Fatalf("missing truncation note: %q", res.Output[len(res.Output)-80:])
	}
}

func TestListDirSuffixAndSort(t *testing.T) {
	_, reg, root := fsRegistry(t)
	if err := os.Mkdir(filepath.Join(root, "zdir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	tool, _ := reg.Get("list_dir")
	act, err := tool.Prepare(`{"path":"."}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatal(res.Output)
	}
	got := strings.Split(res.Output, "\n")
	want := []string{"a.txt", "b.txt", "zdir/"}
	if len(got) != len(want) {
		t.Fatalf("got %q", res.Output)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q want %q", res.Output, strings.Join(want, "\n"))
		}
	}
}
