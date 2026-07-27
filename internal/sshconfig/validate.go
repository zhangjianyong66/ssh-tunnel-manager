package sshconfig

import (
	"errors"
	"strings"
	"unicode"
)

// ValidateAlias rejects values that OpenSSH could interpret as options,
// patterns, or multiple arguments.
func ValidateAlias(alias string) error {
	if alias == "" || strings.HasPrefix(alias, "-") {
		return errors.New("SSH Host 别名无效")
	}
	for _, character := range alias {
		if unicode.IsSpace(character) || unicode.IsControl(character) || strings.ContainsRune("*?!", character) {
			return errors.New("SSH Host 别名无效")
		}
	}
	return nil
}
