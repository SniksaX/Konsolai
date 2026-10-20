package config

import (
	"fmt"
	"strings"
)

// composeVar is a Compose variable that is not a config key.
type composeVar struct {
	name string
	def  string
	use  string
}

func composeVars() []composeVar {
	return []composeVar{
		{"KONSOLAI_UID", "1000", "container user id; `scripts/docker.sh` sets `id -u`"},
		{"KONSOLAI_GID", "1000", "container group id; `scripts/docker.sh` sets `id -g`"},
		{"KONSOLAI_DB_PASSWORD", "konsolai", "local Compose database password"},
		{"KONSOLAI_DB_PORT", "55434", "published host port"},
		{"KONSOLAI_TARGET", "base", "image stage, `base` or `full`"},
		{"KONSOLAI_WORKDIR", ".", "host directory mounted at `/work`"},
		{"KONSOLAI_CONFIG_FILE", "./config.yaml", "config file mounted at `/config/config.yaml`"},
		{"KONSOLAI_EMBED_MODEL", "BAAI/bge-m3", "embedding service model id"},
		{"KONSOLAI_EMBED_PORT", "8080", "published embedding port"},
	}
}

// ComposeVarTable is the README table of Compose-only variables.
func ComposeVarTable() string {
	var b strings.Builder
	b.WriteString("| Variable | Default | Use |\n| --- | --- | --- |\n")
	for _, v := range composeVars() {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", v.name, v.def, v.use)
	}
	return b.String()
}
