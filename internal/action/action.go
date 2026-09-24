package action

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func OpenDirectory(path string) error {
	if path == "" {
		return errors.New("no project directory recorded for this session")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("project directory is missing: %s", Shorten(path, 60))
	}
	if !fi.IsDir() {
		return fmt.Errorf("not a directory: %s", Shorten(path, 60))
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("explorer.exe", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if cmd.Err != nil {
		return fmt.Errorf("%s not found in PATH", cmd.Args[0])
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

func ResumeCommand(sessionID, projectDir string) (*exec.Cmd, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return nil, errors.New("Claude Code executable not found in PATH")
	}
	if sessionID == "" {
		return nil, errors.New("this session has no ID to resume")
	}
	cmd := exec.Command(bin, "--resume", sessionID)
	if fi, err := os.Stat(projectDir); err == nil && fi.IsDir() {
		cmd.Dir = projectDir
	}
	return cmd, nil
}

func Copy(s string) error {
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"pbcopy"}}
	case "windows":
		candidates = [][]string{{"clip.exe"}, {"clip"}}
	default:
		candidates = [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(s)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", c[0], err)
		}
		return nil
	}
	var names []string
	for _, c := range candidates {
		names = append(names, c[0])
	}
	return fmt.Errorf("no clipboard tool found (tried %s)", strings.Join(names, ", "))
}

func Tilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

func Shorten(path string, max int) string {
	p := Tilde(path)
	if len(p) <= max || max < 8 {
		return p
	}
	parts := strings.Split(p, "/")
	for len(parts) > 3 {
		mid := len(parts) / 2
		parts = append(parts[:mid], parts[mid+1:]...)
		parts[len(parts)/2] = "…"
		if joined := strings.Join(parts, "/"); len(joined) <= max {
			return joined
		}
	}
	if p := strings.Join(parts, "/"); len(p) > max {
		return "…" + p[len(p)-max+1:]
	}
	return strings.Join(parts, "/")
}

