package pairing

import (
	"maps"
	"strings"
	"testing"
)

var expectedCookies = map[string]string{
	"SID":     "sid-value",
	"HSID":    "hsid-value",
	"SSID":    "ssid-value",
	"APISID":  "apisid-value",
	"SAPISID": "sapisid/value",
	"OSID":    "osid=value",
}

const cookieHeader = "NID=discarded; SID=sid-value; HSID=hsid-value; SSID=ssid-value; " +
	"APISID=apisid-value; SAPISID=sapisid/value; OSID=osid=value"

func TestParseCookiesFormats(t *testing.T) {
	cases := map[string]string{
		"JSON object": `{"SID":"sid-value","HSID":"hsid-value","SSID":"ssid-value","APISID":"apisid-value",` +
			`"SAPISID":"sapisid/value","OSID":"osid=value","NID":"discarded"}`,
		"bare header":   cookieHeader,
		"header prefix": "Cookie: " + cookieHeader,
		"Chromium cURL with -b": "curl 'https://messages.google.com/web/config' \\\n" +
			"  -H 'accept: */*' \\\n" +
			"  -b '" + cookieHeader + "' \\\n" +
			"  -H 'user-agent: Mozilla/5.0'\n",
		"Firefox cURL with -H": "curl 'https://messages.google.com/web/config' " +
			"-H 'Accept: */*' -H 'Cookie: " + cookieHeader + "' --compressed",
		"double-quoted header": `curl "https://messages.google.com/web/config" -H "cookie: ` + cookieHeader + `"`,
		"ANSI-C quoted cookie": "curl 'https://messages.google.com/web/config' " +
			"-b $'" + strings.Replace(cookieHeader, "sid-value", "sid\\x2dvalue", 1) + "'",
		"CRLF continuations": "curl 'https://messages.google.com/web/config' \\\r\n  -b '" + cookieHeader + "'",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseCookies(input)
			if err != nil {
				t.Fatalf("ParseCookies returned error: %v", err)
			}
			if !maps.Equal(got, expectedCookies) {
				t.Fatalf("ParseCookies = %v, expected %v", got, expectedCookies)
			}
		})
	}
}

func TestParseCookiesRetainsOptionalCookie(t *testing.T) {
	got, err := ParseCookies(cookieHeader + "; __Secure-1PSIDTS=timestamp")
	if err != nil {
		t.Fatalf("ParseCookies returned error: %v", err)
	}
	if got["__Secure-1PSIDTS"] != "timestamp" {
		t.Fatalf("__Secure-1PSIDTS = %q, expected %q", got["__Secure-1PSIDTS"], "timestamp")
	}
}

func TestParseCookiesReportsMissingCookies(t *testing.T) {
	_, err := ParseCookies("SID=a; HSID=b; SSID=c; APISID=d; SAPISID=e")
	if err == nil || !strings.Contains(err.Error(), "OSID") {
		t.Fatalf("ParseCookies error = %v, expected a report of the missing OSID cookie", err)
	}
}

func TestParseCookiesRejectsCurlWithoutCookies(t *testing.T) {
	_, err := ParseCookies("curl 'https://messages.google.com/web/config' -H 'accept: */*'")
	if err != ErrNoCookieHeader {
		t.Fatalf("ParseCookies error = %v, expected ErrNoCookieHeader", err)
	}
}

func TestParseCookiesRejectsUnterminatedQuote(t *testing.T) {
	if _, err := ParseCookies("curl 'https://messages.google.com/web/config' -b 'SID=a"); err == nil {
		t.Fatal("ParseCookies accepted a command with an unterminated quote")
	}
}

func TestParseCookiesRejectsEmptyInput(t *testing.T) {
	if _, err := ParseCookies("  \n"); err == nil {
		t.Fatal("ParseCookies accepted empty input")
	}
}
