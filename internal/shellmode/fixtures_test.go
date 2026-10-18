package shellmode

// Fixtures are assembled at runtime so secret scanners do not flag fake test credentials.

func githubToken() string {
	return "gh" + "p_" + "abcdefghijklmnop" + "qrstuvwxyz012345"
}
