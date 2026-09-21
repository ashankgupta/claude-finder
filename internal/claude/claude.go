package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Session struct {
	ID          string
	ProjectDir  string
	ProjectName string
	Title       string
	FirstPrompt string
	LastPrompt  string
	GitBranch   string

	StartedAt time.Time
	UpdatedAt time.Time

	MessageCount int

	FilePath    string
	FileSize    int64
	FileModTime time.Time

	Messages []Message
}

type Message struct {
	Role      string
	Content   string
	Timestamp time.Time
}

func DefaultRoot() string {
	if d := os.Getenv("CLAUDEFINDER_CLAUDE_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

func Scan(root string) ([]string, error) {
	base := filepath.Join(root, "projects")
	projects, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(base, p.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && isTranscript(e.Name()) {
				out = append(out, filepath.Join(base, p.Name(), e.Name()))
			}
		}
	}
	return out, nil
}

func isTranscript(name string) bool {
	base, ok := strings.CutSuffix(name, ".jsonl")
	return ok && base != "" && !strings.Contains(base, ".")
}

func subagentPaths(sessionPath string) []string {
	dir := strings.TrimSuffix(sessionPath, ".jsonl")
	matches, err := filepath.Glob(filepath.Join(dir, "*", "*.jsonl"))
	if err != nil {
		return nil
	}
	return matches
}

type record struct {
	Type        string `json:"type"`
	SessionID   string `json:"sessionId"`
	Cwd         string `json:"cwd"`
	Timestamp   string `json:"timestamp"`
	GitBranch   string `json:"gitBranch"`
	AiTitle     string `json:"aiTitle"`
	CustomTitle string `json:"customTitle"`
	Summary     string `json:"summary"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Origin      *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	Message       *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

const maxLine = 32 << 20

func eachRecord(path string, fn func(*record)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r record
		if json.Unmarshal(line, &r) != nil {
			continue
		}
		fn(&r)
	}
	return nil
}

func (r *record) message() (Message, bool) {
	if r.Type != "user" && r.Type != "assistant" || r.Message == nil {
		return Message{}, false
	}
	text := extractText(r.Message.Content)
	if text == "" {
		return Message{}, false
	}
	return Message{Role: r.Type, Content: text, Timestamp: parseTime(r.Timestamp)}, true
}

func Parse(path string) (*Session, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	s := &Session{
		FilePath:    path,
		FileSize:    st.Size(),
		FileModTime: st.ModTime().UTC(),
	}

	var aiTitle, customTitle string
	err = eachRecord(path, func(r *record) {
		if s.ID == "" {
			s.ID = r.SessionID
		}
		if s.ProjectDir == "" && r.Cwd != "" {
			s.ProjectDir = r.Cwd
		}
		if s.GitBranch == "" && r.GitBranch != "" {
			s.GitBranch = r.GitBranch
		}
		switch r.Type {
		case "ai-title":
			aiTitle = r.AiTitle
		case "custom-title":
			customTitle = r.CustomTitle
		case "summary":
			if customTitle == "" {
				customTitle = r.Summary
			}
		}

		m, ok := r.message()
		if !ok || (r.Type == "user" && !isHuman(r)) {
			return
		}
		if ts := m.Timestamp; !ts.IsZero() {
			if s.StartedAt.IsZero() || ts.Before(s.StartedAt) {
				s.StartedAt = ts
			}
			if ts.After(s.UpdatedAt) {
				s.UpdatedAt = ts
			}
		}
		s.Messages = append(s.Messages, m)
		if r.IsSidechain {
			return
		}
		s.MessageCount++
		if r.Type == "user" {
			if s.FirstPrompt == "" {
				s.FirstPrompt = Clean(m.Content)
			}
			s.LastPrompt = Clean(m.Content)
		}
	})
	if err != nil {
		return nil, err
	}

	for _, sub := range subagentPaths(path) {
		_ = eachRecord(sub, func(r *record) {
			if m, ok := r.message(); ok {
				s.Messages = append(s.Messages, m)
			}
		})
	}

	if s.ID == "" {
		s.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	if s.ProjectDir == "" {
		s.ProjectDir = guessProjectDir(path)
	}
	s.ProjectName = filepath.Base(s.ProjectDir)
	s.Title = Title(customTitle, aiTitle, s.FirstPrompt)
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = s.FileModTime
	}
	if s.StartedAt.IsZero() {
		s.StartedAt = s.UpdatedAt
	}
	return s, nil
}

var syntheticPrefixes = []string{
	"<local-command-caveat>", "<local-command-stdout>", "<local-command-stderr>",
	"<command-name>", "<command-message>", "<system-reminder>",
	"<user-prompt-submit-hook>", "<ide_selection>",
}

func isHuman(r *record) bool {
	if r.ToolUseResult != nil || r.IsMeta {
		return false
	}
	if r.Origin != nil && r.Origin.Kind != "human" {
		return false
	}
	if r.Message == nil {
		return false
	}
	text := strings.TrimLeftFunc(string(r.Message.Content), func(c rune) bool {
		return c == '"' || c == ' ' || c == '[' || c == '\n'
	})
	for _, p := range syntheticPrefixes {
		if strings.HasPrefix(text, p) {
			return false
		}
	}
	return true
}

func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return strings.TrimSpace(str)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {

		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}

	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func guessProjectDir(path string) string {
	dir := filepath.Base(filepath.Dir(path))
	if !strings.HasPrefix(dir, "-") {
		return dir
	}
	return strings.ReplaceAll(dir, "-", "/")
}

func Title(custom, ai, firstPrompt string) string {
	for _, c := range []string{custom, ai, firstPrompt} {
		if t := Clean(c); t != "" {
			return t
		}
	}
	return "Untitled session"
}

func Clean(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

