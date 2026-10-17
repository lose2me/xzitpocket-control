package config

import "testing"

func TestParseAdminAccounts(t *testing.T) {
	values := map[string]string{
		"CONTROL_ADMIN_KEY":          "super",
		"CONTROL_ADMIN_VIEWER_KEY":   "v",
		"CONTROL_ADMIN_VIEWER_PERMS": "overview=r,users=rw",
		"CONTROL_ADMIN_OPS_KEY":      "o",
		"CONTROL_ADMIN_OPS_PERMS":    "*",
	}
	accounts, err := parseAdminAccounts(values)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 3 {
		t.Fatalf("accounts = %#v", accounts)
	}
	byName := make(map[string]AdminAccount, len(accounts))
	for _, account := range accounts {
		byName[account.Name] = account
	}
	if !byName["admin"].All {
		t.Fatalf("legacy admin should be super: %#v", byName["admin"])
	}
	if byName["viewer"].Perms["overview"] != "r" || byName["viewer"].Perms["users"] != "rw" {
		t.Fatalf("viewer perms = %#v", byName["viewer"].Perms)
	}
	if !byName["ops"].All {
		t.Fatalf("ops wildcard should be super: %#v", byName["ops"])
	}
}

func TestParseAdminPermsRejectsInvalid(t *testing.T) {
	for _, raw := range []string{"overview=x", "nope=r", "overview", "*=r"} {
		if _, err := ParseAdminPerms(raw); err == nil {
			t.Fatalf("ParseAdminPerms(%q) should fail", raw)
		}
	}
}
