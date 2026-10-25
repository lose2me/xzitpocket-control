package config

import "testing"

func TestParseAdminPermsRejectsInvalid(t *testing.T) {
	for _, raw := range []string{"overview=x", "nope=r", "overview", "*=r"} {
		if _, err := ParseAdminPerms(raw); err == nil {
			t.Fatalf("ParseAdminPerms(%q) should fail", raw)
		}
	}
}
