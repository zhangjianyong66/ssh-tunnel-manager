// Package sshconfig reads the selectable Host entries from OpenSSH config files.
package sshconfig

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultMaxIncludeDepth = 32

// Host is a selectable explicit SSH alias and the file where it was declared.
type Host struct {
	Alias  string `json:"alias"`
	Source string `json:"source"`
}

// Diagnostic describes a non-fatal configuration parsing issue.
type Diagnostic struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// Config is the parsed selectable host snapshot.
type Config struct {
	Hosts       []Host       `json:"hosts"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// Loader reads OpenSSH configuration files and recursively expands Include.
type Loader struct {
	MaxIncludeDepth int
}

// Load reads path and all reachable Include files. A missing default config is
// treated as an empty configuration; other filesystem errors are returned.
func (l Loader) Load(path string) (Config, error) {
	if path == "" {
		return Config{}, errors.New("SSH 配置路径不能为空")
	}
	path = expandPath(path, "")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("访问 SSH 配置: %w", err)
	}

	maxDepth := l.MaxIncludeDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxIncludeDepth
	}
	state := parserState{
		maxDepth: maxDepth,
		visited:  make(map[string]bool),
		seen:     make(map[string]bool),
	}
	if err := state.parse(path, 0); err != nil {
		return Config{}, err
	}
	return Config{Hosts: state.hosts, Diagnostics: state.diagnostics}, nil
}

// Load is a convenience wrapper using the default include depth.
func Load(path string) (Config, error) { return (Loader{}).Load(path) }

type parserState struct {
	maxDepth    int
	visited     map[string]bool
	seen        map[string]bool
	hosts       []Host
	diagnostics []Diagnostic
}

func (s *parserState) parse(path string, depth int) error {
	if depth > s.maxDepth {
		s.diagnostics = append(s.diagnostics, Diagnostic{File: path, Message: "Include 嵌套超过最大深度"})
		return nil
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("解析 SSH 配置 %s: %w", path, err)
	}
	if s.visited[path] {
		s.diagnostics = append(s.diagnostics, Diagnostic{File: path, Message: "检测到 Include 循环，已跳过"})
		return nil
	}
	s.visited[path] = true
	defer delete(s.visited, path)

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("读取 SSH 配置 %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if comment := strings.IndexByte(line, '#'); comment >= 0 {
			line = strings.TrimSpace(line[:comment])
			if line == "" {
				continue
			}
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			s.diagnostics = append(s.diagnostics, Diagnostic{File: path, Line: lineNo, Message: "指令缺少参数"})
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "host":
			for _, alias := range fields[1:] {
				if strings.ContainsAny(alias, "*?!") || s.seen[alias] {
					continue
				}
				s.seen[alias] = true
				s.hosts = append(s.hosts, Host{Alias: alias, Source: path})
			}
		case "include":
			for _, pattern := range fields[1:] {
				matches, globErr := filepath.Glob(expandPath(pattern, filepath.Dir(path)))
				if globErr != nil {
					s.diagnostics = append(s.diagnostics, Diagnostic{File: path, Line: lineNo, Message: "Include 通配符无效: " + globErr.Error()})
					continue
				}
				for _, match := range matches {
					if err := s.parse(match, depth+1); err != nil {
						s.diagnostics = append(s.diagnostics, Diagnostic{File: path, Line: lineNo, Message: err.Error()})
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取 SSH 配置 %s: %w", path, err)
	}
	return nil
}

func expandPath(path, base string) string {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	if !filepath.IsAbs(path) && base != "" {
		path = filepath.Join(base, path)
	}
	return filepath.Clean(path)
}
