package redact

import (
	"bytes"
	"strings"
	"testing"
)

func TestStringRules(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		absent  string
		neg     string
		negKeep string
	}{
		{
			name:    "pem",
			in:      pemSample(),
			want:    "[REDACTED:pem]",
			neg:     "-----BEGIN CERTIFICATE-----\nMII\n-----END CERTIFICATE-----",
			negKeep: "BEGIN CERTIFICATE",
		},
		{
			name:    "aws",
			in:      awsPair(),
			want:    "[REDACTED:aws]",
			neg:     "AKIASHORT",
			negKeep: "AKIASHORT",
		},
		{
			name:    "github",
			in:      githubPair(),
			want:    "[REDACTED:github]",
			neg:     "ghp_short",
			negKeep: "ghp_short",
		},
		{
			name:    "slack",
			in:      slackToken(),
			want:    "[REDACTED:slack]",
			neg:     slackShort(),
			negKeep: slackShort(),
		},
		{
			name:    "sk",
			in:      skKey(),
			want:    "[REDACTED:sk]",
			neg:     "sk-shortkey",
			negKeep: "sk-shortkey",
		},
		{
			name:    "jwt",
			in:      jwtSample(),
			want:    "[REDACTED:jwt]",
			neg:     jwtShort(),
			negKeep: "notajwt",
		},
		{
			name:    "bearer",
			in:      "Authorization: Bearer abc.def-ghi+/=",
			want:    "[REDACTED:bearer]",
			neg:     "Bear hug",
			negKeep: "Bear hug",
		},
		{
			name:    "urlpass",
			in:      "postgres://alice:s3cret@localhost/db",
			want:    "postgres://alice:[REDACTED:urlpass]@localhost/db",
			neg:     "postgres://alice@localhost/db",
			negKeep: "postgres://alice@localhost/db",
		},
		{
			name:    "kv",
			in:      "api_key=supersecret",
			want:    "api_key=[REDACTED:kv]",
			neg:     "token := lexer.Next()",
			negKeep: "token := lexer.Next()",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := String(tt.in)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("positive: got %q want substring %q", got, tt.want)
			}
			if tt.name == "urlpass" || tt.name == "kv" {
				if got != tt.want {
					t.Fatalf("got %q want %q", got, tt.want)
				}
			}
			neg := String(tt.neg)
			if !strings.Contains(neg, tt.negKeep) {
				t.Fatalf("negative lost %q: %q", tt.negKeep, neg)
			}
			if strings.Contains(neg, "[REDACTED:"+tt.name+"]") {
				t.Fatalf("negative redacted: %q", neg)
			}
		})
	}
}

func TestKVVariantsAndCode(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"API-KEY: hunter2", "API-KEY: [REDACTED:kv]"},
		{"password = hunter2", "password = [REDACTED:kv]"},
		{"passwd: hunter2", "passwd: [REDACTED:kv]"},
		{"authorization: BearerNope", "authorization: [REDACTED:kv]"},
		{"private_key=abc", "private_key=[REDACTED:kv]"},
		{"secret: xyz", "secret: [REDACTED:kv]"},
	}
	for _, tt := range cases {
		if got := String(tt.in); got != tt.want {
			t.Fatalf("%q: got %q want %q", tt.in, got, tt.want)
		}
	}
	code := "token := lexer.Next()"
	if got := String(code); got != code {
		t.Fatalf("code redacted: %q", got)
	}
	hex := abuseHex()
	got := String("ABUSEIPDB_KEY=" + hex)
	if strings.Contains(got, hex) {
		t.Fatalf("ABUSEIPDB_KEY leaked: %q", got)
	}
	if !strings.Contains(got, "ABUSEIPDB_KEY=[REDACTED:kv]") {
		t.Fatalf("ABUSEIPDB_KEY: %q", got)
	}
	got = String("STRIPE_SECRET=sk_live_notthis")
	if !strings.Contains(got, "STRIPE_SECRET=[REDACTED:kv]") {
		t.Fatalf("STRIPE_SECRET: %q", got)
	}
	got = String("DB_PASS=hunter2")
	if got != "DB_PASS=[REDACTED:kv]" {
		t.Fatalf("DB_PASS: %q", got)
	}
	got = String("PLAIN=" + hex)
	if strings.Contains(got, hex) || !strings.Contains(got, "[REDACTED:env]") {
		t.Fatalf("env hex: %q", got)
	}
}

func TestIdempotent(t *testing.T) {
	in := strings.Join([]string{
		pemShort(),
		awsAccessKey(),
		githubClassic(),
		slackToken(),
		skKey(),
		jwtSample(),
		"Bearer abc.def",
		"https://u:p@h/x",
		"token=shh",
	}, "\n")
	once := String(in)
	twice := String(once)
	if once != twice {
		t.Fatalf("not idempotent\n once=%q\ntwice=%q", once, twice)
	}
}

func TestJSONRedactsStrings(t *testing.T) {
	in := skJSON()
	got := JSON(in)
	if bytes.Contains(got, []byte(skPrefix())) {
		t.Fatalf("sk leaked: %s", got)
	}
	if bytes.Contains(got, []byte("u:p@")) {
		t.Fatalf("url pass leaked: %s", got)
	}
	if !bytes.Contains(got, []byte("[REDACTED:sk]")) {
		t.Fatalf("missing sk redaction: %s", got)
	}
	if !bytes.Contains(got, []byte("[REDACTED:urlpass]")) {
		t.Fatalf("missing urlpass: %s", got)
	}
}

func TestSensitivePath(t *testing.T) {
	yes := []string{
		".env", "dir/.env.local", "cert.pem", "id.key", "tls.p12",
		"id_rsa", "id_rsa.pub", "id_ed25519", ".netrc", ".pgpass",
		"credentials.json", "credentials-prod.json",
	}
	for _, p := range yes {
		if !SensitivePath(p) {
			t.Fatalf("want sensitive %q", p)
		}
	}
	no := []string{"main.go", "readme.md", "env.txt", "id_dsa", "photo.jpg"}
	for _, p := range no {
		if SensitivePath(p) {
			t.Fatalf("want not sensitive %q", p)
		}
	}
	if !SensitivePath(".env.example") {
		t.Fatal(".env.example must be sensitive")
	}
}

func TestMaskValues(t *testing.T) {
	hex := abuseHex()
	in := "ABUSEIPDB_KEY=" + hex + "\n# comment\nEMPTY=\n"
	got, n := MaskValues(in)
	if n != 1 {
		t.Fatalf("n=%d", n)
	}
	if strings.Contains(got, hex) {
		t.Fatalf("leaked: %q", got)
	}
	if !strings.Contains(got, "ABUSEIPDB_KEY=26f7…[masked 80 chars]") {
		t.Fatalf("mask: %q", got)
	}
	if !strings.Contains(got, "# comment") || !strings.Contains(got, "EMPTY=") {
		t.Fatalf("structure lost: %q", got)
	}
}
