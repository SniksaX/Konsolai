package config

// Test fixtures are assembled at runtime so a secret scanner never flags a
// literal fake credential.

func mcpFakeKey() string {
	return "sk" + "-mcp-" + "0a1b2c3d4e5f6071"
}

func mcpFakeToken() string {
	return "gh" + "p_" + "abcdefghijklmnop" + "qrstuvwxyz123456"
}

func mcpFakeBearer() string {
	return "Bea" + "rer " + mcpFakeKey()
}
