package intent

import "testing"

func TestActionFloorFailingREADME(t *testing.T) {
	got, ok := ActionFloor("good can, you create me a README.md for this project ?", nil, nil)
	if !ok || got != Build {
		t.Fatalf("got %q ok=%v want build", got, ok)
	}
}

func TestActionFloorHowExempt(t *testing.T) {
	if _, ok := ActionFloor("how do I create a README?", nil, nil); ok {
		t.Fatal("how-question should be exempt")
	}
}

func TestActionFloorCreateFolder(t *testing.T) {
	got, ok := ActionFloor("crée un dossier test", nil, nil)
	if !ok || got != Act {
		t.Fatalf("got %q ok=%v want act", got, ok)
	}
}

func TestRaiseToNeverLowers(t *testing.T) {
	d := RaiseTo(Decision{Intent: Build}, Act)
	if d.Intent != Build {
		t.Fatalf("%s", d.Intent)
	}
	d = RaiseTo(Decision{Intent: Answer}, Build)
	if d.Intent != Build {
		t.Fatalf("%s", d.Intent)
	}
}

func TestIsPromise(t *testing.T) {
	if !IsPromise("I'll take a look at the repo then create a README.md") {
		t.Fatal("promise")
	}
	if IsPromise("README.md already exists; here is what it contains.") {
		t.Fatal("not a promise")
	}
}

func TestActionFloorKillAndPronoun(t *testing.T) {
	prev := "the go server is listening on port 10101"
	got, ok := ActionFloorPrev("okey delete it", prev, nil, nil, false)
	if !ok || got != Act {
		t.Fatalf("delete it got %q ok=%v", got, ok)
	}
	got, ok = ActionFloorPrev("okey kill it", prev, nil, nil, false)
	if !ok || got != Act {
		t.Fatalf("kill it got %q ok=%v", got, ok)
	}
	if _, ok := ActionFloorPrev("okey delete it", "", nil, nil, false); ok {
		t.Fatal("pronoun without prior object must not floor")
	}
}

func TestActionFloorTestItInRepo(t *testing.T) {
	got, ok := ActionFloorPrev("can you test it ?", "a Go server from main.go", nil, nil, true)
	if !ok || (got != Build && got != Act) {
		t.Fatalf("test it got %q ok=%v", got, ok)
	}
	got, ok = ActionFloorPrev("teste-le", "le serveur tourne", nil, nil, true)
	if !ok || (got != Build && got != Act) {
		t.Fatalf("teste-le got %q ok=%v", got, ok)
	}
}

func TestActionFloorSystemObject(t *testing.T) {
	got, ok := ActionFloor("kill the process on port 8080", nil, nil)
	if !ok || got != Act {
		t.Fatalf("got %q ok=%v", got, ok)
	}
}
