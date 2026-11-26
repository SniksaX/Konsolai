package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/SniksaX/Konsolai/internal/config"
)

// setMCP is the /set mcp command. It only exists as a REPL command; a model
// can never reach it because it is not registered as a tool.
func setMCP(env *Env, args []string, session bool) int {
	if env == nil {
		return 1
	}
	if len(args) == 0 {
		return showMCP(env)
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "add":
		return mcpAdd(env, args[1:], session)
	case "remove":
		return mcpRemove(env, args[1:], session)
	case "enable", "disable":
		return mcpSetEnabled(env, args[0], args[1:], session)
	case "trust":
		return mcpSetTrust(env, args[1:], session)
	case "allow", "deny":
		return mcpSetToolGlob(env, args[0], args[1:], session)
	case "header":
		return mcpSetValue(env, "header", args[1:], session)
	case "env":
		return mcpSetValue(env, "env", args[1:], session)
	default:
		fmt.Fprintln(env.errw(), mcpSetUsage)
		return 2
	}
}

const mcpSetUsage = "usage: /set mcp [add stdio <name> <command> [args...] | add http <name> <url> | remove|enable|disable|trust|allow|deny <name> ... | header|env <name> <KEY> <value>]"

func showMCP(env *Env) int {
	c := env.Profile
	fmt.Fprintf(env.out(), "max_inline_tools: %d\n", c.MCPInlineCap())
	names := c.MCPServerNames()
	if len(names) == 0 {
		fmt.Fprintln(env.out(), "no MCP servers configured")
		return 0
	}
	for _, name := range names {
		s, _ := c.MCPServer(name)
		state := "disabled"
		if s.Enabled {
			state = "enabled"
		}
		private := ""
		if s.Private {
			private = ", private"
		}
		fmt.Fprintf(env.out(), "%s  %s  %s, trust %s, timeout %dms%s\n",
			name, s.Transport, state, s.TrustMode(), s.Timeout(), private)
	}
	return 0
}

func mcpAdd(env *Env, args []string, session bool) int {
	if len(args) < 3 {
		fmt.Fprintln(env.errw(), mcpSetUsage)
		return 2
	}
	transport := strings.ToLower(strings.TrimSpace(args[0]))
	name := strings.TrimSpace(args[1])
	if !config.ValidMCPServerName(name) {
		fmt.Fprintf(env.errw(), "invalid MCP server name %q\n", name)
		return 2
	}
	if _, exists := env.Profile.MCPServer(name); exists {
		fmt.Fprintf(env.errw(), "MCP server %q already exists; remove it first\n", name)
		return 2
	}
	s := config.DefaultMCPServer()
	switch transport {
	case config.MCPTransportStdio:
		s.Transport = config.MCPTransportStdio
		s.Command = args[2]
		s.Args = append([]string{}, args[3:]...)
		line := mcpCommandLine(s)
		fmt.Fprintf(env.out(), "command: %s\n", line)
		if !env.Yes {
			ok, err := confirmReset(env, fmt.Sprintf("add stdio server %q and run `%s`?", name, line))
			if err != nil {
				fmt.Fprintln(env.errw(), err)
				return 1
			}
			if !ok {
				fmt.Fprintln(env.out(), "left unchanged")
				return 1
			}
		}
	case config.MCPTransportHTTP:
		s.Transport = config.MCPTransportHTTP
		s.URL = args[2]
	default:
		fmt.Fprintln(env.errw(), "the transport must be stdio or http")
		return 2
	}
	if code := mcpApply(env, name, s, session); code != 0 {
		return code
	}
	fmt.Fprintf(env.out(), "added %s (%s, trust %s)\n", name, s.Transport, s.TrustMode())
	return 0
}

func mcpRemove(env *Env, args []string, session bool) int {
	if len(args) != 1 {
		fmt.Fprintln(env.errw(), "usage: /set mcp remove <name>")
		return 2
	}
	name := strings.TrimSpace(args[0])
	if _, ok := env.Profile.MCPServer(name); !ok {
		fmt.Fprintf(env.errw(), "no MCP server %q\n", name)
		return 2
	}
	c := env.Profile
	if c.MCP.Servers != nil {
		delete(c.MCP.Servers, name)
	}
	env.Profile = c
	if env.Runner != nil {
		env.Runner.Profile = c
	}
	if !session {
		if err := config.UpsertMCPServer(configPath(env), name, config.MCPServer{}, true); err != nil {
			fmt.Fprintln(env.errw(), err)
			return 1
		}
	}
	fmt.Fprintf(env.out(), "removed %s\n", name)
	return 0
}

func mcpSetEnabled(env *Env, verb string, args []string, session bool) int {
	if len(args) != 1 {
		fmt.Fprintf(env.errw(), "usage: /set mcp %s <name>\n", verb)
		return 2
	}
	name := strings.TrimSpace(args[0])
	s, ok := env.Profile.MCPServer(name)
	if !ok {
		fmt.Fprintf(env.errw(), "no MCP server %q\n", name)
		return 2
	}
	s.Enabled = verb == "enable"
	if code := mcpApply(env, name, s, session); code != 0 {
		return code
	}
	state := "disabled"
	if s.Enabled {
		state = "enabled"
	}
	fmt.Fprintf(env.out(), "%s %s\n", state, name)
	return 0
}

func mcpSetTrust(env *Env, args []string, session bool) int {
	if len(args) != 2 {
		fmt.Fprintln(env.errw(), "usage: /set mcp trust <name> ask|read_only|auto")
		return 2
	}
	name := strings.TrimSpace(args[0])
	level := strings.ToLower(strings.TrimSpace(args[1]))
	switch level {
	case config.MCPTrustAsk, config.MCPTrustReadOnly, config.MCPTrustAuto:
	default:
		fmt.Fprintf(env.errw(), "trust must be ask, read_only or auto, got %q\n", args[1])
		return 2
	}
	s, ok := env.Profile.MCPServer(name)
	if !ok {
		fmt.Fprintf(env.errw(), "no MCP server %q\n", name)
		return 2
	}
	s.Trust = level
	if code := mcpApply(env, name, s, session); code != 0 {
		return code
	}
	fmt.Fprintf(env.out(), "%s trust %s\n", name, level)
	return 0
}

func mcpSetToolGlob(env *Env, verb string, args []string, session bool) int {
	if len(args) != 2 {
		fmt.Fprintf(env.errw(), "usage: /set mcp %s <name> <glob>\n", verb)
		return 2
	}
	name := strings.TrimSpace(args[0])
	glob := strings.TrimSpace(args[1])
	if err := config.ValidateToolGlob(glob); err != nil {
		fmt.Fprintln(env.errw(), err)
		return 2
	}
	s, ok := env.Profile.MCPServer(name)
	if !ok {
		fmt.Fprintf(env.errw(), "no MCP server %q\n", name)
		return 2
	}
	if verb == "allow" {
		if !containsString(s.Tools.Allow, glob) {
			s.Tools.Allow = append(append([]string{}, s.Tools.Allow...), glob)
		}
	} else {
		if !containsString(s.Tools.Deny, glob) {
			s.Tools.Deny = append(append([]string{}, s.Tools.Deny...), glob)
		}
	}
	if code := mcpApply(env, name, s, session); code != 0 {
		return code
	}
	fmt.Fprintf(env.out(), "%s %s %s\n", verb, name, glob)
	return 0
}

// mcpSetValue sets one env or header value. A literal secret is refused; a
// ${ENV_VAR} reference (alone or inside a scheme such as Bearer) is allowed.
func mcpSetValue(env *Env, kind string, args []string, session bool) int {
	if len(args) != 3 {
		fmt.Fprintf(env.errw(), "usage: /set mcp %s <name> <KEY> <value>\n", kind)
		return 2
	}
	name := strings.TrimSpace(args[0])
	key := strings.TrimSpace(args[1])
	value := args[2]
	s, ok := env.Profile.MCPServer(name)
	if !ok {
		fmt.Fprintf(env.errw(), "no MCP server %q\n", name)
		return 2
	}
	var err error
	if kind == "env" {
		err = config.ValidateEnvName(key)
	} else {
		err = config.ValidateHeaderName(key)
	}
	if err != nil {
		fmt.Fprintln(env.errw(), err)
		return 2
	}
	if config.LiteralSecret(value) {
		fmt.Fprintf(env.errw(), "refusing a literal secret in %s %s %s; use ${ENV_VAR}\n", kind, name, key)
		return 2
	}
	if kind == "env" {
		envMap := make(map[string]string, len(s.Env)+1)
		for k, v := range s.Env {
			envMap[k] = v
		}
		envMap[key] = value
		s.Env = envMap
	} else {
		headerMap := make(map[string]string, len(s.Headers)+1)
		for k, v := range s.Headers {
			headerMap[k] = v
		}
		headerMap[key] = value
		s.Headers = headerMap
	}
	if code := mcpApply(env, name, s, session); code != 0 {
		return code
	}
	fmt.Fprintf(env.out(), "%s %s %s\n", kind, name, key)
	return 0
}

// mcpApply validates the whole config, updates the profile and writes the
// server into the file unless the change is for this REPL session only.
func mcpApply(env *Env, name string, server config.MCPServer, session bool) int {
	c := env.Profile
	servers := make(map[string]config.MCPServer, len(c.MCP.Servers)+1)
	for key, value := range c.MCP.Servers {
		servers[key] = value
	}
	servers[name] = server
	c.MCP.Servers = servers
	if err := config.Validate(c); err != nil {
		fmt.Fprintln(env.errw(), err)
		return 2
	}
	env.Profile = c
	if env.Runner != nil {
		env.Runner.Profile = c
	}
	if !session {
		if err := config.UpsertMCPServer(configPath(env), name, server, false); err != nil {
			fmt.Fprintln(env.errw(), err)
			return 1
		}
	}
	return 0
}

// mcpCommandLine renders the exact stdio command line for display.
func mcpCommandLine(s config.MCPServer) string {
	parts := []string{s.Command}
	for _, arg := range s.Args {
		if strings.ContainsAny(arg, " \t\n'\"") {
			parts = append(parts, strconv.Quote(arg))
			continue
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

// mcpSetLevel is the /set mcp argument tree.
func mcpSetLevel(*Env) []Choice {
	return []Choice{
		{
			Value: "add", Description: "Add a stdio or http server",
			Next: []Choice{
				{Value: "stdio", Description: "Run a local command over stdio"},
				{Value: "http", Description: "Connect to a streamable HTTP URL"},
			},
		},
		{Value: "remove", Description: "Remove a configured server"},
		{Value: "enable", Description: "Enable a server"},
		{Value: "disable", Description: "Disable a server"},
		{
			Value: "trust", Description: "Set the tool trust level",
			Next: []Choice{
				{Value: config.MCPTrustAsk, Description: "Ask before a mutating tool runs"},
				{Value: config.MCPTrustReadOnly, Description: "Allow read-only tools only"},
				{Value: config.MCPTrustAuto, Description: "Trust the server's read-only hint; mutating calls still ask"},
			},
		},
		{Value: "allow", Description: "Add an allow glob for tools"},
		{Value: "deny", Description: "Add a deny glob for tools"},
		{Value: "header", Description: "Set a header from ${ENV_VAR} or a plain value"},
		{Value: "env", Description: "Set an environment variable from ${ENV_VAR} or a plain value"},
	}
}
