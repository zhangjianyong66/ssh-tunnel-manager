package hostconfig

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// Render creates an OpenSSH config from validated profiles and a read-only
// system Include. It performs no filesystem writes.
func Render(profiles []Profile, systemPath string) ([]byte, error) {
	if systemPath == "" || !filepath.IsAbs(systemPath) {
		return nil, errors.New("系统 SSH 配置路径必须是绝对路径")
	}
	var output bytes.Buffer
	for _, profile := range profiles {
		normalized, err := normalizeProfile(profile)
		if err != nil {
			return nil, err
		}
		output.WriteString("Host ")
		output.WriteString(normalized.Alias)
		output.WriteByte('\n')
		writeOption(&output, "HostName", normalized.HostName)
		writeOption(&output, "Port", strconv.FormatUint(uint64(normalized.Port), 10))
		writeOption(&output, "User", normalized.Username)
		if normalized.IdentityFile != "" {
			writeOption(&output, "IdentityFile", normalized.IdentityFile)
		}
		jump := normalized.JumpHost
		if jump == "" {
			jump = "none"
		}
		output.WriteString("    ProxyJump ")
		output.WriteString(jump)
		output.WriteByte('\n')
		output.WriteByte('\n')
	}
	output.WriteString("Include ")
	output.WriteString(quoteConfigValue(filepath.Clean(systemPath)))
	output.WriteByte('\n')
	return output.Bytes(), nil
}

func writeOption(output *bytes.Buffer, name, value string) {
	output.WriteString("    ")
	output.WriteString(name)
	output.WriteByte(' ')
	output.WriteString(quoteConfigValue(value))
	output.WriteByte('\n')
}

func quoteConfigValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return fmt.Sprintf("\"%s\"", value)
}
