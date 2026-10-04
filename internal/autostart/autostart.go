// Package autostart starts the agent at login: a per-user LaunchAgent on
// macOS, a HKCU Run value on Windows. Enabling or disabling never starts or
// stops the running agent.
package autostart

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// Manager turns starting at login on or off.
type Manager interface {
	Enable() error
	Disable() error
	Enabled() (bool, error)
	// Target returns the executable the stored entry starts (an error when
	// there is no entry), so a stale path can be noticed.
	Target() (string, error)
}

// Label is the macOS LaunchAgent label (and plist file name).
const Label = "com.niceit.s3-anysync.agent"

// RunValue is the Windows HKCU Run value name.
const RunValue = "s3-anysync"

func xmlEscape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// plistContent is the LaunchAgent that runs exe with args at login.
func plistContent(exe string, args []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	b.WriteString("\t<key>Label</key>\n\t<string>" + Label + "</string>\n")
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range append([]string{exe}, args...) {
		b.WriteString("\t\t<string>" + xmlEscape(a) + "</string>\n")
	}
	b.WriteString("\t</array>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>ProcessType</key>\n\t<string>Interactive</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// commandLine quotes exe and args the way Windows (CommandLineToArgvW) parses them.
// Example: `C:\dir with space\` becomes "C:\dir with space\\".
func commandLine(exe string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	for _, a := range append([]string{exe}, args...) {
		parts = append(parts, quoteArg(a))
	}
	return strings.Join(parts, " ")
}

func quoteArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			slashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, slashes*2+1))
			slashes = 0
			b.WriteByte(c)
			continue
		default:
			b.WriteString(strings.Repeat(`\`, slashes))
			slashes = 0
			b.WriteByte(c)
			continue
		}
	}
	b.WriteString(strings.Repeat(`\`, slashes*2))
	b.WriteByte('"')
	return b.String()
}

// plistTarget returns the first ProgramArguments string of a LaunchAgent plist.
func plistTarget(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var text strings.Builder
	var lastKey string
	inArray, inText := false, false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return "", errors.New("plist has no ProgramArguments")
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "key", "string":
				inText = true
				text.Reset()
			case "array":
				inArray = lastKey == "ProgramArguments"
			}
		case xml.CharData:
			if inText {
				text.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "key":
				lastKey, inText = text.String(), false
			case "string":
				if inArray {
					return text.String(), nil
				}
				inText = false
			}
		}
	}
}

// commandLineTarget returns the first token of a Windows command line.
func commandLineTarget(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) {
		rest := cmd[1:]
		if i := strings.IndexByte(rest, '"'); i >= 0 {
			return rest[:i]
		}
		return rest
	}
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		return cmd[:i]
	}
	return cmd
}
