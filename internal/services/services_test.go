package services

import (
	"context"
	"testing"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/sysexec"
)

func TestUnitStateOnlyQueriesKnownUnits(t *testing.T) {
	fake := &sysexec.Fake{
		Installed: map[string]bool{"systemctl": true},
		Outputs: map[string][]byte{
			"systemctl is-active cloudflared.service":          []byte("inactive\n"),
			"systemctl is-active smb.service":                  []byte("inactive\n"),
			"systemctl --user is-active omarchy-vault.service": []byte("active\n"),
		},
	}
	comps := Check(context.Background(), fake)
	known := map[string]bool{}
	for _, c := range Known() {
		if c.Unit != "" {
			known[c.Unit] = true
		}
	}
	for _, call := range fake.Calls {
		ok := false
		for u := range known {
			if len(call) >= len(u) && call[len(call)-len(u):] == u {
				ok = true
			}
		}
		if !ok {
			t.Errorf("unexpected systemctl call %q", call)
		}
	}
	for _, c := range comps {
		if c.ID == "cloudflared" && c.UnitState != "inactive" {
			t.Errorf("cloudflared state = %q", c.UnitState)
		}
	}
	for _, c := range MarkFiles(comps, true, true, "/home/me/.local/share/omarchy-vault/sftpgo/bin/sftpgo") {
		if c.ID == "sftpgo" && (!c.Installed || c.UnitState != "active") {
			t.Errorf("files = %+v", c)
		}
	}
	for _, c := range MarkSelfRunning(comps) {
		if c.ID == "vault" && (!c.Installed || c.UnitState != "active") {
			t.Errorf("vault self state = %+v", c)
		}
	}
}
