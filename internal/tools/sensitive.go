package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SniksaX/Konsolai/internal/safety"
)

// sensitiveWrite classifies one resolved write target. It returns "" for a
// normal write, a reason for a write that must be confirmed separately, or an
// error for git metadata the model may never write.
func sensitiveWrite(guard *safety.Guard, extra []string, resolved, display, oldText, newText string) (string, error) {
	if guard == nil {
		return "", nil
	}
	class, reason := safety.ClassifyWriteAt(guard.Root(), resolved, extra)
	if strings.EqualFold(filepath.Base(resolved), "package.json") {
		if sensitive, diff, err := safety.PackageJSONChange(oldText, newText); err == nil {
			if sensitive {
				return "this file runs code later: package.json " + diff, nil
			}
			// Dependency-only change: keep it normal even if a broad extra glob matched.
			return "", nil
		}
	}
	switch class {
	case safety.WriteNever:
		return "", fmt.Errorf("this is git metadata; the user changes it themselves")
	case safety.WriteConfirm:
		return reason, nil
	}
	return "", nil
}

func withExtraSensitive[T any](tool T, extra []string) T {
	return tool
}

// readExisting returns the current text for a target, if it exists.
func readExisting(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// appendSensitivePreview adds the separate-confirmation reason to an action preview.
func appendSensitivePreview(preview, reason string) string {
	if strings.TrimSpace(reason) == "" {
		return preview
	}
	if strings.TrimSpace(preview) == "" {
		return reason
	}
	return reason + "\n\n" + preview
}
