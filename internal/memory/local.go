package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/redact"
)

const (
	lastSessionFile = "last_session.json"
	lastSessionKeep = 20
)

type LastSession struct {
	Profile   string        `json:"profile"`
	CWD       string        `json:"cwd"`
	Title     string        `json:"title"`
	UpdatedAt time.Time     `json:"updated_at"`
	Messages  []llm.Message `json:"messages"`
}

func StateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "konsolai")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "konsolai")
	}
	return filepath.Join(home, ".local", "state", "konsolai")
}

func LastPath() string {
	return filepath.Join(StateDir(), lastSessionFile)
}

func redactMessage(m llm.Message) llm.Message {
	m.Content = redact.String(m.Content)
	if len(m.ToolCalls) == 0 {
		return m
	}
	calls := make([]llm.ToolCall, len(m.ToolCalls))
	copy(calls, m.ToolCalls)
	for i := range calls {
		calls[i].Function.Arguments = string(redact.JSON([]byte(calls[i].Function.Arguments)))
	}
	m.ToolCalls = calls
	return m
}

func keepNonSystem(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == llm.RoleSystem {
			continue
		}
		out = append(out, redactMessage(m))
	}
	if len(out) > lastSessionKeep {
		out = out[len(out)-lastSessionKeep:]
	}
	return out
}

func LoadLast(profile, cwd string, idle time.Duration) (LastSession, bool) {
	b, err := os.ReadFile(LastPath())
	if err != nil {
		return LastSession{}, false
	}
	var s LastSession
	if err := json.Unmarshal(b, &s); err != nil {
		return LastSession{}, false
	}
	if s.Profile != profile || s.CWD != cwd {
		return LastSession{}, false
	}
	if idle > 0 && !s.UpdatedAt.IsZero() && time.Since(s.UpdatedAt) > idle {
		return LastSession{}, false
	}
	return s, true
}

func ClearLast() error {
	err := os.Remove(LastPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func SaveLast(s LastSession) error {
	dir := StateDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	s.UpdatedAt = time.Now()
	s.Messages = keepNonSystem(s.Messages)
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, lastSessionFile+".tmp")
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, LastPath()); err != nil {
		return err
	}
	return os.Chmod(LastPath(), 0600)
}
