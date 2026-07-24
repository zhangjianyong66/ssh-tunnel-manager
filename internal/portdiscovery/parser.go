package portdiscovery

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const maxParserDiagnostics = 32

// Port is one remote TCP listening port. Address is kept for internal
// consumers but is intentionally omitted from JSON until address selection is
// part of the product contract.
type Port struct {
	Number  uint16 `json:"number"`
	Process string `json:"process,omitempty"`
	Address string `json:"-"`
}

// Parse converts ss -ltn and ss -ltnp table output into stable port records.
// Unsupported or malformed rows are skipped and returned as generic,
// non-sensitive diagnostics.
func Parse(output string) ([]Port, []string) {
	byPort := make(map[uint16]Port)
	var diagnostics []string
	for lineNumber, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.EqualFold(fields[0], "state") {
			continue
		}
		if len(fields) < 5 || fields[0] != "LISTEN" {
			if len(fields) > 0 {
				diagnostics = appendDiagnostic(diagnostics, fmt.Sprintf("第 %d 行不是可识别的 TCP 监听记录", lineNumber+1))
			}
			continue
		}
		port, ok := parsePort(fields[3])
		if !ok {
			diagnostics = appendDiagnostic(diagnostics, fmt.Sprintf("第 %d 行端口无效", lineNumber+1))
			continue
		}
		candidate := Port{Number: port, Address: fields[3], Process: processName(fields[5:])}
		current, exists := byPort[port]
		if !exists || (current.Process == "" && candidate.Process != "") {
			byPort[port] = candidate
		}
	}
	ports := make([]Port, 0, len(byPort))
	for _, port := range byPort {
		ports = append(ports, port)
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Number < ports[j].Number })
	return ports, diagnostics
}

func parsePort(address string) (uint16, bool) {
	index := strings.LastIndexByte(address, ':')
	if index < 0 || index == len(address)-1 {
		return 0, false
	}
	value, err := strconv.Atoi(address[index+1:])
	if err != nil || value < 1 || value > 65535 {
		return 0, false
	}
	return uint16(value), true
}

func processName(fields []string) string {
	value := strings.Join(fields, " ")
	const marker = `users:(("`
	start := strings.Index(value, marker)
	if start < 0 {
		return ""
	}
	value = value[start+len(marker):]
	end := strings.IndexByte(value, '"')
	if end <= 0 {
		return ""
	}
	name := value[:end]
	if strings.ContainsAny(name, "\r\n\x00") {
		return ""
	}
	return name
}

func appendDiagnostic(diagnostics []string, message string) []string {
	if len(diagnostics) < maxParserDiagnostics {
		return append(diagnostics, message)
	}
	return diagnostics
}
