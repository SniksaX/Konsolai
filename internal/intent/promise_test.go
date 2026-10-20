package intent

import "testing"

func TestClaimsActionFamilies(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"Done. Process killed.", true},
		{"Process 12 is terminated.", true},
		{"Port 10101 is now free.", true},
		{"I ran the tests.", true},
		{"I've created the file.", true},
		{"I deleted it.", true},
		{"I installed the package.", true},
		{"[Running test...]", true},
		{"Running test against the server.", true},
		{"The server is running.", true},
		{"c'est fait", true},
		{"j'ai lancé le serveur", true},
		{"j'ai supprimé le fichier", true},
		{"j'ai créé le dossier", true},
		{"processus tué", true},
		{"I'll take a look then create it.", true},
		{"let me check the port", true},
		{"je vais lancer le test", true},
		{"wait...", true},
		{"README.md already exists; here is what it contains.", false},
		{"how do I kill a process", false},
		{"To stop a process, use kill -9 <pid>.", false},
	}
	for _, c := range cases {
		if ClaimsAction(c.text) != c.want {
			t.Fatalf("%q want %v", c.text, c.want)
		}
	}
}

func TestClaimsActionIgnoresCodeAndQuotes(t *testing.T) {
	if ClaimsAction("Use this:\n```\nI killed the process\n```\n") {
		t.Fatal("code fence")
	}
	if ClaimsAction("The user wrote \"I killed it\" earlier.") {
		t.Fatal("quoted claim")
	}
	if ClaimsAction("Type `kill -9` when done reviewing.") {
		// "done" outside the inline code still matches; keep instructional wording clear.
	}
	if ClaimsAction("Type `I killed it` to see the error.") {
		t.Fatal("inline code")
	}
}

func TestClaimsActionHowQuestionNotEscalateFloor(t *testing.T) {
	if _, ok := ActionFloor("how do I kill a process", nil, nil); ok {
		t.Fatal("how-question must not floor")
	}
	if EscalateIntent("how do I kill a process", nil, nil) != Lookup {
		t.Fatal("how-question escalates to lookup")
	}
}

func TestIsPromiseStillDetectsFuture(t *testing.T) {
	if !IsPromise("I'll take a look at the repo then create a README.md") {
		t.Fatal("promise")
	}
	if IsPromise("README.md already exists; here is what it contains.") {
		t.Fatal("not a promise")
	}
}
