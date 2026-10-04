// Command pecheck prints the Windows subsystem (gui or console) of PE files.
package main

import (
	"debug/pe"
	"fmt"
	"os"
)

func subsystem(path string) (string, error) {
	f, err := pe.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var sub uint16
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		sub = h.Subsystem
	case *pe.OptionalHeader32:
		sub = h.Subsystem
	default:
		return "", fmt.Errorf("%s: no optional header", path)
	}
	switch sub {
	case pe.IMAGE_SUBSYSTEM_WINDOWS_GUI:
		return "gui", nil
	case pe.IMAGE_SUBSYSTEM_WINDOWS_CUI:
		return "console", nil
	}
	return fmt.Sprintf("subsystem-%d", sub), nil
}

func main() {
	failed := false
	for _, p := range os.Args[1:] {
		s, err := subsystem(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			failed = true
			continue
		}
		fmt.Printf("%s: %s\n", p, s)
	}
	if failed {
		os.Exit(1)
	}
}
