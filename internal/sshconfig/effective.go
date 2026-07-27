package sshconfig

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const maxEffectiveConfigOutput = 1 << 20

// Effective is the subset of resolved OpenSSH options needed to validate a
// system Host before using it as a managed jump host.
type Effective struct {
	HostName      string
	Port          uint16
	User          string
	IdentityFiles []string
	ProxyJump     string
	ProxyCommand  string
}

// EffectiveResolver delegates OpenSSH matching semantics to ssh -G.
type EffectiveResolver struct {
	Binary string
	Run    func(context.Context, string, []string) ([]byte, error)
}

// Resolve returns effective options for alias without opening a network
// connection or starting authentication.
func (r EffectiveResolver) Resolve(ctx context.Context, configPath, alias string) (Effective, error) {
	if configPath == "" {
		return Effective{}, errors.New("SSH 配置路径不能为空")
	}
	if err := ValidateAlias(alias); err != nil {
		return Effective{}, err
	}
	binary := r.Binary
	if binary == "" {
		binary = "ssh"
	}
	args := []string{"-G", "-F", configPath, "--", alias}
	run := r.Run
	if run == nil {
		run = runEffectiveCommand
	}
	value, err := run(ctx, binary, args)
	if err != nil {
		return Effective{}, fmt.Errorf("解析 SSH Host 有效配置失败: %w", err)
	}
	if len(value) > maxEffectiveConfigOutput {
		return Effective{}, errors.New("SSH Host 有效配置输出过大")
	}
	return parseEffective(value)
}

func runEffectiveCommand(ctx context.Context, binary string, args []string) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("%s", message)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

func parseEffective(value []byte) (Effective, error) {
	var result Effective
	for _, line := range strings.Split(string(value), "\n") {
		key, text, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		switch strings.ToLower(key) {
		case "hostname":
			result.HostName = text
		case "port":
			port, err := strconv.ParseUint(text, 10, 16)
			if err != nil || port == 0 {
				return Effective{}, errors.New("SSH Host 有效端口无效")
			}
			result.Port = uint16(port)
		case "user":
			result.User = text
		case "identityfile":
			result.IdentityFiles = append(result.IdentityFiles, text)
		case "proxyjump":
			result.ProxyJump = text
		case "proxycommand":
			result.ProxyCommand = text
		}
	}
	if result.HostName == "" || result.Port == 0 || result.User == "" {
		return Effective{}, errors.New("SSH Host 有效配置缺少必要字段")
	}
	return result, nil
}
