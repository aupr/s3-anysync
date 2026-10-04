package autostart

import (
	"encoding/xml"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestPlistContent(t *testing.T) {
	p := plistContent("/Applications/s3 & sync.app/Contents/MacOS/s3-anysync-agent", []string{"run"})
	for _, want := range []string{
		"<key>Label</key>", "<string>com.niceit.s3-anysync.agent</string>",
		"<string>/Applications/s3 &amp; sync.app/Contents/MacOS/s3-anysync-agent</string>", "<string>run</string>",
		"<key>RunAtLoad</key>", "<true/>", "<key>ProcessType</key>", "<string>Interactive</string>",
		"<key>ProgramArguments</key>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q:\n%s", want, p)
		}
	}
	if !strings.HasPrefix(p, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Error("missing XML header")
	}
}

func TestPlistIsValidXML(t *testing.T) {
	p := plistContent(`/a/<b>&"c'`, []string{"x<y", "&amp;"})
	dec := xml.NewDecoder(strings.NewReader(p))
	dec.Strict = true
	var strs []string
	inString := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid XML: %v\n%s", err, p)
		}
		switch v := tok.(type) {
		case xml.StartElement:
			inString = v.Name.Local == "string"
		case xml.EndElement:
			inString = false
		case xml.CharData:
			if inString {
				strs = append(strs, string(v))
			}
		}
	}
	want := []string{Label, `/a/<b>&"c'`, "x<y", "&amp;", "Interactive"}
	if !reflect.DeepEqual(strs, want) {
		t.Errorf("strings = %q, want %q", strs, want)
	}
}

func TestCommandLine(t *testing.T) {
	cases := map[string]struct {
		exe  string
		args []string
	}{
		`C:\s3\agent.exe run`:                  {`C:\s3\agent.exe`, []string{"run"}},
		`"C:\Program Files\s3\agent.exe" run`:  {`C:\Program Files\s3\agent.exe`, []string{"run"}},
		`C:\a.exe "two words" "say \"hi\"" ""`: {`C:\a.exe`, []string{"two words", `say "hi"`, ""}},
		`"C:\dir with space\\" run`:            {`C:\dir with space\`, []string{"run"}},
	}
	for want, c := range cases {
		if got := commandLine(c.exe, c.args); got != want {
			t.Errorf("commandLine(%q, %q) = %s, want %s", c.exe, c.args, got, want)
		}
	}
}

// parseArgv is a reference implementation of CommandLineToArgvW's rules for
// arguments after the program name (the program name is parsed the same way
// here since commandLine quotes it identically).
func parseArgv(s string) []string {
	var out []string
	i, n := 0, len(s)
	for {
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= n {
			return out
		}
		var cur strings.Builder
		inQ := false
		for i < n {
			c := s[i]
			if c == '\\' {
				j := i
				for j < n && s[j] == '\\' {
					j++
				}
				k := j - i
				if j < n && s[j] == '"' {
					cur.WriteString(strings.Repeat(`\`, k/2))
					if k%2 == 1 {
						cur.WriteByte('"')
						j++
					}
					// even: quote handled on next iteration as toggle
				} else {
					cur.WriteString(strings.Repeat(`\`, k))
				}
				i = j
				continue
			}
			if c == '"' {
				inQ = !inQ
				i++
				continue
			}
			if (c == ' ' || c == '\t') && !inQ {
				break
			}
			cur.WriteByte(c)
			i++
		}
		out = append(out, cur.String())
	}
}

func TestCommandLineRoundTrip(t *testing.T) {
	exes := []string{`C:\s3\agent.exe`, `C:\Program Files\s3\agent.exe`, `C:\dir with space\`, `C:\x\\`, `\\srv\share\a b\a.exe`}
	argSets := [][]string{
		nil,
		{"run"},
		{""},
		{"two words", `say "hi"`, ""},
		{`trailing\`, `trailing space\ `, `a b\`, `a b\\`},
		{`\"`, `\\"`, `"`, `""`, `a\"b`, `a\\"b c`, `tab	x`},
		{`C:\path with space\dir\`, `--flag=va lue`, `\\\`},
	}
	for _, exe := range exes {
		for _, args := range argSets {
			line := commandLine(exe, args)
			got := parseArgv(line)
			want := append([]string{exe}, args...)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("round trip of %q %q: line %s parsed to %q", exe, args, line, got)
			}
		}
	}
}
