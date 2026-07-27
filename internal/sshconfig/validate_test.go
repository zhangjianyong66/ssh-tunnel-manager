package sshconfig

import "testing"

func TestValidateAlias(t *testing.T) {
	for _, alias := range []string{"server-a", "user@example", "192.0.2.1"} {
		if err := ValidateAlias(alias); err != nil {
			t.Errorf("ValidateAlias(%q): %v", alias, err)
		}
	}
	for _, alias := range []string{"", "-oProxyCommand=bad", "two hosts", "*.example", "host?", "host!"} {
		if err := ValidateAlias(alias); err == nil {
			t.Errorf("ValidateAlias(%q) succeeded", alias)
		}
	}
}
