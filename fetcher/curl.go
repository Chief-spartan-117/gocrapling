package fetcher

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// ParseCurl converts a curl command line (as copied from browser DevTools
// "Copy as cURL") into a Request. Unsupported flags that take no effect on
// the request (-s, -L, -k, --compressed, ...) are ignored.
func ParseCurl(cmd string) (Request, error) {
	toks, err := shellSplit(cmd)
	if err != nil {
		return Request{}, err
	}
	if len(toks) > 0 && toks[0] == "curl" {
		toks = toks[1:]
	}
	req := Request{Headers: map[string]string{}}
	var data []string
	var asQuery bool
	var user string

	next := func(i *int, flag string) (string, error) {
		if *i+1 >= len(toks) {
			return "", fmt.Errorf("curl: %s needs a value", flag)
		}
		*i++
		return toks[*i], nil
	}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		// --flag=value form
		flag, inline, hasInline := t, "", false
		if strings.HasPrefix(t, "--") {
			if k, v, ok := strings.Cut(t, "="); ok {
				flag, inline, hasInline = k, v, true
			}
		}
		val := func() (string, error) {
			if hasInline {
				return inline, nil
			}
			return next(&i, flag)
		}
		switch flag {
		case "-X", "--request":
			v, err := val()
			if err != nil {
				return req, err
			}
			req.Method = strings.ToUpper(v)
		case "-H", "--header":
			v, err := val()
			if err != nil {
				return req, err
			}
			k, hv, ok := strings.Cut(v, ":")
			if !ok {
				return req, fmt.Errorf("curl: bad header %q", v)
			}
			req.Headers[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(hv)
		case "-d", "--data", "--data-raw", "--data-binary", "--data-ascii", "--data-urlencode":
			v, err := val()
			if err != nil {
				return req, err
			}
			data = append(data, v)
		case "--json":
			v, err := val()
			if err != nil {
				return req, err
			}
			data = append(data, v)
			req.Headers["content-type"] = "application/json"
			req.Headers["accept"] = "application/json"
		case "-A", "--user-agent":
			v, err := val()
			if err != nil {
				return req, err
			}
			req.Headers["user-agent"] = v
		case "-e", "--referer":
			v, err := val()
			if err != nil {
				return req, err
			}
			req.Referer = v
		case "-b", "--cookie":
			v, err := val()
			if err != nil {
				return req, err
			}
			req.Headers["cookie"] = v
		case "-u", "--user":
			v, err := val()
			if err != nil {
				return req, err
			}
			user = v
		case "--url":
			v, err := val()
			if err != nil {
				return req, err
			}
			req.URL = v
		case "-G", "--get":
			asQuery = true
		case "-x", "--proxy":
			v, err := val()
			if err != nil {
				return req, err
			}
			req.Proxy = v
		case "-m", "--max-time", "-o", "--output", "-w", "--write-out", "--connect-timeout", "--retry":
			if _, err := val(); err != nil {
				return req, err
			}
		default:
			if strings.HasPrefix(t, "-") && t != "-" {
				continue // boolean flag with no effect here
			}
			if req.URL == "" {
				req.URL = t
			}
		}
	}
	if req.URL == "" {
		return req, errors.New("curl: no URL")
	}
	if user != "" {
		req.Headers["authorization"] = "Basic " + base64Std(user)
	}
	body := strings.Join(data, "&")
	switch {
	case asQuery && body != "":
		sep := "?"
		if strings.Contains(req.URL, "?") {
			sep = "&"
		}
		req.URL += sep + body
		if req.Method == "" {
			req.Method = "GET"
		}
	case body != "":
		req.Body = []byte(body)
		if req.Method == "" {
			req.Method = "POST"
		}
		if _, ok := req.Headers["content-type"]; !ok {
			req.Headers["content-type"] = "application/x-www-form-urlencoded"
		}
	}
	if req.Method == "" {
		req.Method = "GET"
	}
	if _, err := url.Parse(req.URL); err != nil {
		return req, fmt.Errorf("curl: bad url: %w", err)
	}
	return req, nil
}

func base64Std(s string) string {
	const tbl = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := []byte(s)
	var sb strings.Builder
	for i := 0; i < len(b); i += 3 {
		var n uint32
		rem := len(b) - i
		for j := 0; j < 3; j++ {
			n <<= 8
			if j < rem {
				n |= uint32(b[i+j])
			}
		}
		for j := 0; j < 4; j++ {
			if j <= rem {
				sb.WriteByte(tbl[(n>>(18-6*uint(j)))&63])
			} else {
				sb.WriteByte('=')
			}
		}
	}
	return sb.String()
}

// shellSplit tokenises like a POSIX shell: single quotes are literal,
// double quotes allow backslash escapes, $'...' (ANSI-C, as DevTools emits)
// decodes \n \t \\ \' \" and \xNN, and backslash-newline continues a line.
func shellSplit(s string) ([]string, error) {
	var toks []string
	var cur strings.Builder
	inTok := false
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\\' && i+1 < len(rs) && (rs[i+1] == '\n' || rs[i+1] == '\r'):
			i++
			if rs[i] == '\r' && i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
		case c == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			inTok = true
		case c == '\'':
			j := i + 1
			for j < len(rs) && rs[j] != '\'' {
				j++
			}
			if j >= len(rs) {
				return nil, errors.New("curl: unterminated single quote")
			}
			cur.WriteString(string(rs[i+1 : j]))
			i, inTok = j, true
		case c == '$' && i+1 < len(rs) && rs[i+1] == '\'':
			j := i + 2
			for ; j < len(rs) && rs[j] != '\''; j++ {
				if rs[j] == '\\' && j+1 < len(rs) {
					j++
					switch rs[j] {
					case 'n':
						cur.WriteByte('\n')
					case 't':
						cur.WriteByte('\t')
					case 'r':
						cur.WriteByte('\r')
					case 'x':
						if j+2 < len(rs) {
							var v int
							fmt.Sscanf(string(rs[j+1:j+3]), "%x", &v)
							cur.WriteByte(byte(v))
							j += 2
						}
					default:
						cur.WriteRune(rs[j])
					}
					continue
				}
				cur.WriteRune(rs[j])
			}
			if j >= len(rs) {
				return nil, errors.New("curl: unterminated $'...' quote")
			}
			i, inTok = j, true
		case c == '"':
			j := i + 1
			for ; j < len(rs) && rs[j] != '"'; j++ {
				if rs[j] == '\\' && j+1 < len(rs) && strings.ContainsRune(`"\$`+"`", rs[j+1]) {
					j++
				}
				cur.WriteRune(rs[j])
			}
			if j >= len(rs) {
				return nil, errors.New("curl: unterminated double quote")
			}
			i, inTok = j, true
		case unicode.IsSpace(c):
			if inTok {
				toks = append(toks, cur.String())
				cur.Reset()
				inTok = false
			}
		default:
			cur.WriteRune(c)
			inTok = true
		}
	}
	if inTok {
		toks = append(toks, cur.String())
	}
	return toks, nil
}
