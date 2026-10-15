package llm

import "testing"

func TestThinkFilterSplitTags(t *testing.T) {
	var f ThinkFilter
	got := f.Write("<thi")
	got += f.Write("nk>x</th")
	got += f.Write("ink>\n\nls")
	got += f.Flush()
	if got != "ls" {
		t.Fatalf("got %q want %q", got, "ls")
	}
}

func TestThinkFilterMidText(t *testing.T) {
	var f ThinkFilter
	got := f.Write("hello <think>secret</think> world")
	got += f.Flush()
	if got != "hello  world" {
		t.Fatalf("got %q want %q", got, "hello  world")
	}
}

func TestThinkFilterUnterminatedFlush(t *testing.T) {
	var f ThinkFilter
	_ = f.Write("ok\n<think>hidden")
	if got := f.Flush(); got != "" {
		t.Fatalf("Flush got %q, want empty", got)
	}
}

func TestThinkFilterNearTag(t *testing.T) {
	var f ThinkFilter
	got := f.Write("a<b")
	got += f.Flush()
	if got != "a<b" {
		t.Fatalf("got %q want %q", got, "a<b")
	}
}
