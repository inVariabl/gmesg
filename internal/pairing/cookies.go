package pairing

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// AcceptedCookies lists the Google account cookies the pairing flow uses,
// matching the cookie fields mautrix-gmessages' own Google login requests.
// Every other cookie in the input is discarded so the session file holds
// no more credential material than necessary.
var AcceptedCookies = []string{"SID", "HSID", "SSID", "APISID", "SAPISID", "OSID", "__Secure-1PSIDTS"}

// RequiredCookies lists the cookies without which Google rejects the
// account sign-in. OSID is scoped to messages.google.com, so it is present
// only in a request sent to that host.
var RequiredCookies = []string{"SID", "HSID", "SSID", "APISID", "SAPISID", "OSID"}

// ErrNoCookieHeader is returned when a cURL command carries neither a -b
// argument nor a Cookie header.
var ErrNoCookieHeader = errors.New("the cURL command contains no cookies; copy a request sent to messages.google.com")

// ParseCookies extracts Google account cookies from one of three input
// formats: a JSON object mapping cookie names to values, a cURL command as
// produced by a browser's developer tools ("Copy as cURL"), or a bare
// Cookie header value ("SID=...; HSID=..."), optionally prefixed with
// "Cookie:". The result holds only AcceptedCookies, and an error is
// returned if any of RequiredCookies is absent.
func ParseCookies(input string) (map[string]string, error) {
	input = strings.TrimSpace(input)
	var all map[string]string
	switch {
	case input == "":
		return nil, errors.New("no cookie input was provided")
	case strings.HasPrefix(input, "{"):
		if err := json.Unmarshal([]byte(input), &all); err != nil {
			return nil, fmt.Errorf("parse cookie JSON object: %w", err)
		}
	case strings.HasPrefix(input, "curl "):
		header, err := cookieHeaderFromCurl(input)
		if err != nil {
			return nil, err
		}
		all = parseCookieHeader(header)
	default:
		if name, value, ok := strings.Cut(input, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "cookie") {
			input = value
		}
		all = parseCookieHeader(input)
	}

	cookies := make(map[string]string, len(AcceptedCookies))
	for _, name := range AcceptedCookies {
		if value := all[name]; value != "" {
			cookies[name] = value
		}
	}
	var missing []string
	for _, name := range RequiredCookies {
		if _, ok := cookies[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("required cookies are missing: %s", strings.Join(missing, ", "))
	}
	return cookies, nil
}

// parseCookieHeader splits a Cookie header value into name/value pairs.
func parseCookieHeader(header string) map[string]string {
	cookies := make(map[string]string)
	for _, pair := range strings.Split(header, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || name == "" {
			continue
		}
		cookies[name] = value
	}
	return cookies
}

// cookieHeaderFromCurl returns the cookie string carried by a cURL
// command, either as the argument of -b/--cookie (current Chromium
// releases) or as a "Cookie:" header given to -H/--header (Firefox and
// earlier Chromium releases).
func cookieHeaderFromCurl(command string) (string, error) {
	args, err := splitShellWords(command)
	if err != nil {
		return "", fmt.Errorf("parse cURL command: %w", err)
	}
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-b", "--cookie":
			return args[i+1], nil
		case "-H", "--header":
			name, value, ok := strings.Cut(args[i+1], ":")
			if ok && strings.EqualFold(strings.TrimSpace(name), "cookie") {
				return value, nil
			}
		}
	}
	return "", ErrNoCookieHeader
}

// splitShellWords divides a POSIX shell command line into arguments. It
// supports the constructs browsers emit in "Copy as cURL" output: single
// quotes, double quotes, ANSI-C $'...' quotes, backslash escapes, and
// backslash-newline line continuations. It performs no expansion.
func splitShellWords(line string) ([]string, error) {
	var (
		args    []string
		current strings.Builder
		inWord  bool
	)
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inWord {
				args = append(args, current.String())
				current.Reset()
				inWord = false
			}
			i++
		case c == '\\':
			if i+1 < len(line) && line[i+1] == '\n' {
				i += 2
				continue
			}
			if i+2 < len(line) && line[i+1] == '\r' && line[i+2] == '\n' {
				i += 3
				continue
			}
			if i+1 < len(line) {
				current.WriteByte(line[i+1])
			}
			inWord = true
			i += 2
		case c == '\'':
			end := strings.IndexByte(line[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("unterminated single quote")
			}
			current.WriteString(line[i+1 : i+1+end])
			inWord = true
			i += end + 2
		case c == '$' && i+1 < len(line) && line[i+1] == '\'':
			n, err := readAnsiCQuoted(line[i+2:], &current)
			if err != nil {
				return nil, err
			}
			inWord = true
			i += 2 + n
		case c == '"':
			n, err := readDoubleQuoted(line[i+1:], &current)
			if err != nil {
				return nil, err
			}
			inWord = true
			i += 1 + n
		default:
			current.WriteByte(c)
			inWord = true
			i++
		}
	}
	if inWord {
		args = append(args, current.String())
	}
	return args, nil
}

// readDoubleQuoted consumes the body of a double-quoted string, starting
// just after the opening quote, and returns the number of bytes consumed
// including the closing quote.
func readDoubleQuoted(s string, out *strings.Builder) (int, error) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			return i + 1, nil
		case '\\':
			if i+1 < len(s) && strings.IndexByte("\"\\$`\n", s[i+1]) >= 0 {
				if s[i+1] != '\n' {
					out.WriteByte(s[i+1])
				}
				i++
				continue
			}
			out.WriteByte('\\')
		default:
			out.WriteByte(s[i])
		}
	}
	return 0, errors.New("unterminated double quote")
}

// readAnsiCQuoted consumes the body of a $'...' string, starting just
// after the opening quote, and returns the number of bytes consumed
// including the closing quote.
func readAnsiCQuoted(s string, out *strings.Builder) (int, error) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			return i + 1, nil
		case '\\':
			if i+1 >= len(s) {
				return 0, errors.New("unterminated ANSI-C quote")
			}
			i++
			switch e := s[i]; e {
			case 'n':
				out.WriteByte('\n')
			case 't':
				out.WriteByte('\t')
			case 'r':
				out.WriteByte('\r')
			case 'x', 'u', 'U':
				width := map[byte]int{'x': 2, 'u': 4, 'U': 8}[e]
				digits := 0
				var value rune
				for digits < width && i+1+digits < len(s) && isHexDigit(s[i+1+digits]) {
					value = value*16 + hexValue(s[i+1+digits])
					digits++
				}
				if digits == 0 {
					out.WriteByte('\\')
					out.WriteByte(e)
					continue
				}
				if e == 'x' {
					out.WriteByte(byte(value))
				} else {
					out.WriteRune(value)
				}
				i += digits
			default:
				if slices.Contains([]byte("\\'\"?"), e) {
					out.WriteByte(e)
				} else {
					out.WriteByte('\\')
					out.WriteByte(e)
				}
			}
		default:
			out.WriteByte(s[i])
		}
	}
	return 0, errors.New("unterminated ANSI-C quote")
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexValue(c byte) rune {
	switch {
	case c >= '0' && c <= '9':
		return rune(c - '0')
	case c >= 'a' && c <= 'f':
		return rune(c-'a') + 10
	default:
		return rune(c-'A') + 10
	}
}
