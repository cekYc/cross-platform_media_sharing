package discord

import "testing"

func TestParseRoleIDs(t *testing.T) {
	raw := "  role-1,role-2 , , role-3  "
	parsed := parseRoleIDs(raw)

	if len(parsed) != 3 {
		t.Fatalf("expected 3 role ids, got %d", len(parsed))
	}

	for _, roleID := range []string{"role-1", "role-2", "role-3"} {
		if _, ok := parsed[roleID]; !ok {
			t.Fatalf("expected role id %q to be present", roleID)
		}
	}
}

func TestIsManagedCommand(t *testing.T) {
	managed := []string{"!join", "!unlink", "!status", "!help", "!blocklist", "!unblock", "!clearblocks", "!deadletters", "!replaydead", "!setrule", "!auditlog"}
	for _, command := range managed {
		if !isManagedCommand(command) {
			t.Fatalf("expected %q to be managed", command)
		}
	}

	notManaged := []string{"", "join", "!random", "/help", "status"}
	for _, command := range notManaged {
		if isManagedCommand(command) {
			t.Fatalf("expected %q to be unmanaged", command)
		}
	}
}
