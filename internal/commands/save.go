package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/SniksaX/Konsolai/internal/docrender"
	"github.com/SniksaX/Konsolai/internal/repo"
	"github.com/SniksaX/Konsolai/internal/safety"
)

var renderChrome = docrender.FindChrome

func runSave(ctx context.Context, env *Env, args []string) int {
	return writeAnswerFile(ctx, env, args, "md")
}

func runPDF(ctx context.Context, env *Env, args []string) int {
	return writeAnswerFile(ctx, env, args, "pdf")
}

func writeAnswerFile(ctx context.Context, env *Env, args []string, format string) int {
	file, force, errMsg := splitForce(args)
	usage := "usage: /save [file]"
	if format == "pdf" {
		usage = "usage: /pdf [file]"
	}
	if errMsg != "" {
		fmt.Fprintln(env.errw(), usage)
		return 2
	}
	title, body, ok := latestAnswer(ctx, env)
	if !ok || strings.TrimSpace(body) == "" {
		fmt.Fprintln(env.errw(), "no answers")
		return 1
	}
	if file == "" {
		ext := ".md"
		if format == "pdf" {
			ext = ".pdf"
		}
		file = slugTitle(title) + ext
	}
	g, err := answerGuard(env)
	if err != nil {
		fmt.Fprintln(env.errw(), err)
		return 1
	}
	resolved, err := g.ResolvePath(file)
	if err != nil {
		fmt.Fprintln(env.errw(), err)
		return 1
	}
	if _, err := os.Stat(resolved); err == nil && !force {
		fmt.Fprintf(env.errw(), "refusing to overwrite %s (pass --force)\n", file)
		return 1
	} else if err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(env.errw(), err)
		return 1
	}
	var data []byte
	if format == "pdf" {
		if _, err := renderChrome(); err != nil {
			fmt.Fprintf(env.errw(), "pdf skipped: %s\n", err)
			return 1
		}
		rendered, err := docrender.Render(ctx, "pdf", file, body)
		if err != nil {
			fmt.Fprintln(env.errw(), err)
			return 1
		}
		data = rendered.Bytes
	} else {
		data = []byte(body)
		if !strings.HasSuffix(body, "\n") {
			data = append(data, '\n')
		}
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
		fmt.Fprintln(env.errw(), err)
		return 1
	}
	if err := os.WriteFile(resolved, data, 0644); err != nil {
		fmt.Fprintln(env.errw(), err)
		return 1
	}
	fmt.Fprintf(env.out(), "saved %s (%s)\n", file, formatSize(len(data)))
	return 0
}

func splitForce(args []string) (file string, force bool, errMsg string) {
	var files []string
	for _, a := range args {
		if a == "--force" {
			force = true
			continue
		}
		files = append(files, a)
	}
	if len(files) > 1 {
		return "", false, "usage"
	}
	if len(files) == 1 {
		file = files[0]
	}
	return file, force, ""
}

func latestAnswer(ctx context.Context, env *Env) (title, body string, ok bool) {
	for _, it := range loadHistoryFixed(ctx, env, 5) {
		ps := pairsFromMsgs(it.Msgs)
		if len(ps) == 0 {
			continue
		}
		return it.Title, ps[len(ps)-1].Answer, true
	}
	return "", "", false
}

func answerGuard(env *Env) (*safety.Guard, error) {
	cwd := env.Cwd
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("commands: cwd: %w", err)
		}
	}
	return safety.NewAt(repo.Root(cwd), cwd)
}

func slugTitle(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "answer"
	}
	if len(name) > 60 {
		name = strings.Trim(name[:60], "-")
	}
	if name == "" {
		name = "answer"
	}
	return name
}
