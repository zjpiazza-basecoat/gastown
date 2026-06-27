package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStewardUpgradeScriptIsInstallOnly(t *testing.T) {
	forbidden := []string{
		"go test",
		"go install",
		"go build",
		"git pull",
		"git fetch",
		"git stash",
	}
	for _, needle := range forbidden {
		if strings.Contains(stewardUpgradeScript, needle) {
			t.Fatalf("upgrade install script should not contain %q", needle)
		}
	}
	for _, needle := range []string{"last_green", "$ARTIFACTS/gt-", "$ARTIFACTS/bd-", "install_atomically"} {
		if !strings.Contains(stewardUpgradeScript, needle) {
			t.Fatalf("upgrade install script missing %q", needle)
		}
	}
}

func TestEnsureStewardScriptWritesInstallOnlyUpgradeScript(t *testing.T) {
	townRoot := t.TempDir()
	if _, err := ensureStewardScript(townRoot); err != nil {
		t.Fatalf("ensureStewardScript: %v", err)
	}
	path := filepath.Join(townRoot, "scripts", "gt-upgrade-local.sh")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read upgrade script: %v", err)
	}
	content := string(b)
	if !strings.Contains(content, "Installing previously tested binaries") {
		t.Fatalf("upgrade script does not describe install-only behavior:\n%s", content)
	}
	if strings.Contains(content, "go test") || strings.Contains(content, "git pull") {
		t.Fatalf("upgrade script contains validation/update commands:\n%s", content)
	}
}
