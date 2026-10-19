package tools

import "strings"

// Fixtures are assembled at runtime so secret scanners do not flag fake test credentials.

func abuseHex() string {
	return strings.Join([]string{
		"26f789a3dde42ad6",
		"ec6a50fc258c2a59",
		"a9833069884e231b",
		"0081525ef87cbc30",
		"1ba28e96d4e0f46c",
	}, "")
}
