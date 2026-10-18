package redact

import "strings"

// Fixtures are assembled at runtime so secret scanners do not flag fake test credentials.

func pemSample() string {
	begin := "-----" + "BEGIN " + "RSA " + "PRIVATE KEY" + "-----"
	end := "-----" + "END " + "RSA " + "PRIVATE KEY" + "-----"
	return "x\n" + begin + "\nMIIEowIBAAKCAQ\n" + end + "\ny"
}

func pemShort() string {
	begin := "-----" + "BEGIN " + "RSA " + "PRIVATE KEY" + "-----"
	end := "-----" + "END " + "RSA " + "PRIVATE KEY" + "-----"
	return begin + "\nMII\n" + end
}

func awsAccessKey() string {
	return "AK" + "IA" + "IOSFODNN7EXAMPLE"
}

func awsTempKey() string {
	return "AS" + "IA" + "IOSFODNN7EXAMPLE"
}

func awsPair() string {
	return "id " + awsAccessKey() + " " + awsTempKey() + " extra"
}

func githubClassic() string {
	return "gh" + "p_" + "abcdefghijklmnop" + "qrstuvwxyz123456"
}

func githubPat() string {
	return "github" + "_pat_" + "abcdefghijkl" + "mnopqrstuvwxyz"
}

func githubPair() string {
	return "tok " + githubClassic() + " " + githubPat() + " extra"
}

func slackToken() string {
	return "xo" + "xb-" + "1234567890-" + "abcdefghij"
}

func slackShort() string {
	return "xo" + "xb-" + "short"
}

func skKey() string {
	return "sk" + "-" + "abcdefghijklmnop" + "qrstuvwxyz123456"
}

func skPrefix() string {
	return "sk" + "-" + "abcdefghijklmnop" + "qrst"
}

func jwtSample() string {
	head := "ey" + "JhbGciOiJIUzI1NiJ9"
	payload := "ey" + "JzdWIiOiIxIn0"
	return head + "." + payload + "." + "abc_def-ghi"
}

func jwtShort() string {
	return "ey" + "JhbGciOiJIUzI1NiJ9" + ".notajwt"
}

func abuseHex() string {
	return strings.Join([]string{
		"26f789a3dde42ad6",
		"ec6a50fc258c2a59",
		"a9833069884e231b",
		"0081525ef87cbc30",
		"1ba28e96d4e0f46c",
	}, "")
}

func skJSON() []byte {
	return []byte(`{"cmd":"echo","token":"` + skKey() + `","nested":{"url":"https://u:p@h/x"},"n":1}`)
}
