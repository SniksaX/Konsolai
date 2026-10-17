package ui

import "testing"

func TestSetKeyReaderUsedByConfirm(t *testing.T) {
	ch := make(chan byte, 1)
	ch <- 'y'
	SetKeyReader(func() (byte, error) {
		return <-ch, nil
	})
	t.Cleanup(func() { SetKeyReader(nil) })
	p, err := OpenPrompter(false)
	if err != nil {
		t.Fatal(err)
	}
	p.out = discard{}
	d, _, err := p.Confirm("go", "")
	if err != nil || d != Approve {
		t.Fatalf("%v %v", d, err)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
