package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/formula"
)

func writeGTStub(t *testing.T, binDir string, unixScript string, windowsScript string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		path := filepath.Join(binDir, "gt.cmd")
		if err := os.WriteFile(path, []byte(windowsScript), 0o644); err != nil {
			t.Fatalf("write gt stub: %v", err)
		}
		return path
	}

	path := filepath.Join(binDir, "gt")
	if err := os.WriteFile(path, []byte(unixScript), 0o755); err != nil {
		t.Fatalf("write gt stub: %v", err)
	}
	return path
}

// TestAutoInferRig verifies the rig auto-selection logic used when --rig is
// not provided and cwd-based detection finds nothing (e.g. Deacon at HQ level
// on a non-default install where "gastown" rig does not exist).
func TestAutoInferRig(t *testing.T) {
	t.Parallel()

	makeWorkspace := func(t *testing.T) (root string) {
		t.Helper()
		root = t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "mayor"), 0o755); err != nil {
			t.Fatalf("mkdir mayor: %v", err)
		}
		return root
	}

	writeRigsJSON := func(t *testing.T, root string, rigNames []string) {
		t.Helper()
		cfg := &config.RigsConfig{
			Version: 1,
			Rigs:    make(map[string]config.RigEntry),
		}
		for _, name := range rigNames {
			cfg.Rigs[name] = config.RigEntry{}
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("marshal rigs.json: %v", err)
		}
		path := filepath.Join(root, "mayor", "rigs.json")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write rigs.json: %v", err)
		}
	}

	t.Run("single rig auto-selects", func(t *testing.T) {
		t.Parallel()
		root := makeWorkspace(t)
		rigDir := filepath.Join(root, "myrig")
		if err := os.MkdirAll(rigDir, 0o755); err != nil {
			t.Fatalf("mkdir myrig: %v", err)
		}
		writeRigsJSON(t, root, []string{"myrig"})

		name, path, err := autoInferRig(root)
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if name != "myrig" {
			t.Errorf("name = %q, want %q", name, "myrig")
		}
		if path != rigDir {
			t.Errorf("path = %q, want %q", path, rigDir)
		}
	})

	t.Run("multiple rigs require explicit --rig", func(t *testing.T) {
		t.Parallel()
		root := makeWorkspace(t)
		for _, name := range []string{"rig1", "rig2"} {
			if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", name, err)
			}
		}
		writeRigsJSON(t, root, []string{"rig1", "rig2"})

		_, _, err := autoInferRig(root)
		if err == nil {
			t.Fatal("expected error for multiple rigs, got nil")
		}
		if !strings.Contains(err.Error(), "cannot determine target rig") {
			t.Errorf("expected rig-detection error, got: %v", err)
		}
		if !strings.Contains(err.Error(), "--rig=NAME") {
			t.Errorf("error should suggest --rig=NAME, got: %v", err)
		}
		if !strings.Contains(err.Error(), "rig1") || !strings.Contains(err.Error(), "rig2") {
			t.Errorf("error should list available rigs, got: %v", err)
		}
	})

	t.Run("no rigs registered", func(t *testing.T) {
		t.Parallel()
		root := makeWorkspace(t)
		writeRigsJSON(t, root, []string{})

		_, _, err := autoInferRig(root)
		if err == nil {
			t.Fatal("expected error for no rigs, got nil")
		}
		if !strings.Contains(err.Error(), "no rigs registered") {
			t.Errorf("error should mention no rigs registered, got: %v", err)
		}
		if !strings.Contains(err.Error(), "--rig=NAME") {
			t.Errorf("error should suggest --rig=NAME, got: %v", err)
		}
	})

	t.Run("malformed rigs.json surfaces error", func(t *testing.T) {
		t.Parallel()
		root := makeWorkspace(t)
		path := filepath.Join(root, "mayor", "rigs.json")
		if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
			t.Fatalf("write rigs.json: %v", err)
		}

		// discoverRigsForTownRoot silently falls back to an empty config on
		// parse error, so autoInferRig surfaces the "no rigs registered" path.
		_, _, err := autoInferRig(root)
		if err == nil {
			t.Fatal("expected error for malformed rigs.json, got nil")
		}
		if !strings.Contains(err.Error(), "no rigs registered") {
			t.Errorf("expected no-rigs error (fallback from malformed JSON), got: %v", err)
		}
	})
}

func TestBuildConvoyLegSlingArgs_AlwaysIncludesNoConvoy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		agent      string
		reviewOnly bool
		wantFlags  []string
	}{
		{"no agent no review", "", false, []string{"--no-convoy"}},
		{"with agent", "claude", false, []string{"--no-convoy", "--agent", "claude"}},
		{"review only", "", true, []string{"--no-convoy", "--review-only"}},
		{"agent and review", "gemini", true, []string{"--no-convoy", "--agent", "gemini", "--review-only"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildConvoyLegSlingArgs("bead-1", "myrig", "desc", "title", tt.agent, tt.reviewOnly)
			for _, want := range tt.wantFlags {
				if !slices.Contains(got, want) {
					t.Errorf("buildConvoyLegSlingArgs() missing %q in %v", want, got)
				}
			}
			if got[0] != "sling" {
				t.Errorf("first arg must be 'sling', got %q", got[0])
			}
		})
	}
}

func TestExecuteConvoyFormulaRoutesRootToHQAndLegsToTargetRig(t *testing.T) {
	townRoot := t.TempDir()
	townBeadsDir := filepath.Join(townRoot, ".beads")
	rigDir := filepath.Join(townRoot, "gastown", "mayor", "rig")
	rigBeadsDir := filepath.Join(rigDir, ".beads")
	workDir := filepath.Join(townRoot, "gastown", "polecats", "pipboy", "gastown")
	for _, dir := range []string{filepath.Join(townRoot, "mayor"), townBeadsDir, rigBeadsDir, workDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(townRoot, "mayor", "town.json"), []byte(`{"name":"test"}`), 0o644); err != nil {
		t.Fatalf("write town.json: %v", err)
	}
	routes := strings.Join([]string{
		`{"prefix":"gt-","path":"gastown/mayor/rig"}`,
		`{"prefix":"hq-","path":"."}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(townBeadsDir, "routes.jsonl"), []byte(routes), 0o644); err != nil {
		t.Fatalf("write routes: %v", err)
	}

	binDir := filepath.Join(townRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	bdLog := filepath.Join(townRoot, "bd.log")
	gtLog := filepath.Join(townRoot, "gt.log")
	bdScript := `#!/bin/sh
printf '%s|%s|%s\n' "$(pwd)" "${BEADS_DIR:-}" "$*" >> "$BD_LOG"
exit 0
`
	bdScriptWindows := `@echo off
echo %CD%^|%BEADS_DIR%^|%*>>"%BD_LOG%"
exit /b 0
`
	gtScript := `#!/bin/sh
printf '%s|%s\n' "$(pwd)" "$*" >> "$GT_LOG"
exit 0
`
	gtScriptWindows := `@echo off
echo %CD%^|%*>>"%GT_LOG%"
exit /b 0
`
	writeBDStub(t, binDir, bdScript, bdScriptWindows)
	writeGTStub(t, binDir, gtScript, gtScriptWindows)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BD_LOG", bdLog)
	t.Setenv("GT_LOG", gtLog)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("chdir workDir: %v", err)
	}

	oldAddTracking := addTrackingRelationFn
	t.Cleanup(func() { addTrackingRelationFn = oldAddTracking })
	var tracked []string
	addTrackingRelationFn = func(root, trackerID, issueID string) error {
		tracked = append(tracked, root+"|"+trackerID+"|"+issueID)
		return nil
	}

	f := &formula.Formula{
		Description: "review changes",
		Legs: []formula.Leg{{
			ID:          "readability",
			Title:       "Readability",
			Focus:       "structure",
			Description: "Review structure",
		}},
		Synthesis: &formula.Synthesis{Title: "Synthesize"},
	}
	if err := executeConvoyFormula(f, "code-review", "gastown"); err != nil {
		t.Fatalf("executeConvoyFormula: %v", err)
	}

	bdBytes, err := os.ReadFile(bdLog)
	if err != nil {
		t.Fatalf("read bd log: %v", err)
	}
	bdText := string(bdBytes)
	if !strings.Contains(bdText, townBeadsDir+"|") || !strings.Contains(bdText, "--id=hq-cv-") {
		t.Fatalf("bd log missing HQ convoy create: %s", bdText)
	}
	if !strings.Contains(bdText, rigBeadsDir+"|") || !strings.Contains(bdText, "--id=gt-leg-") {
		t.Fatalf("bd log missing target-rig leg create: %s", bdText)
	}
	if !strings.Contains(bdText, rigBeadsDir+"|") || !strings.Contains(bdText, "--id=gt-syn-") {
		t.Fatalf("bd log missing target-rig synthesis create: %s", bdText)
	}
	if len(tracked) != 2 {
		t.Fatalf("tracked relations = %v, want leg and synthesis", tracked)
	}
	for _, got := range tracked {
		if !strings.HasPrefix(got, townRoot+"|hq-cv-") {
			t.Fatalf("tracking relation used wrong town root or tracker ID: %v", tracked)
		}
	}
}

func TestBuildWorkflowStepSlingArgs_AlwaysIncludesNoConvoy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		agent string
	}{
		{"no agent", ""},
		{"with agent", "claude-haiku"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildWorkflowStepSlingArgs("bead-2", "myrig", "desc", "title", tt.agent)
			if !slices.Contains(got, "--no-convoy") {
				t.Errorf("buildWorkflowStepSlingArgs() missing --no-convoy in %v", got)
			}
			if got[0] != "sling" {
				t.Errorf("first arg must be 'sling', got %q", got[0])
			}
			if tt.agent != "" && !slices.Contains(got, tt.agent) {
				t.Errorf("buildWorkflowStepSlingArgs() missing agent %q in %v", tt.agent, got)
			}
		})
	}
}

func TestResolveFormulaLegAgent_Precedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		legAgent     string
		cliAgent     string
		formulaAgent string
		want         string
	}{
		{"all empty", "", "", "", ""},
		{"formula only", "", "", "gemini", "gemini"},
		{"cli only", "", "codex", "", "codex"},
		{"leg only", "claude-haiku", "", "", "claude-haiku"},
		{"cli overrides formula", "", "codex", "gemini", "codex"},
		{"leg overrides cli", "claude-haiku", "codex", "gemini", "claude-haiku"},
		{"leg overrides formula", "claude-haiku", "", "gemini", "claude-haiku"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolveFormulaLegAgent(tt.legAgent, tt.cliAgent, tt.formulaAgent)
			if got != tt.want {
				t.Errorf("resolveFormulaLegAgent(%q, %q, %q) = %q, want %q",
					tt.legAgent, tt.cliAgent, tt.formulaAgent, got, tt.want)
			}
		})
	}
}

func TestSubstituteFormulaVars(t *testing.T) {
	t.Parallel()

	vars := map[string]interface{}{
		"problem": "First paragraph.\n\nSecond paragraph.",
		"context": "existing code",
	}
	got := substituteFormulaVars("Problem: {{ problem }}\nContext: {{context}}\nKeep: {{review_id}}", vars)
	want := "Problem: First paragraph.\n\nSecond paragraph.\nContext: existing code\nKeep: {{review_id}}"
	if got != want {
		t.Fatalf("substituteFormulaVars() = %q, want %q", got, want)
	}
}

func TestParseSetVarsPreservesMultilineValues(t *testing.T) {
	t.Parallel()

	got := parseSetVars([]string{"problem=First\n\nSecond", "context=a=b"})
	if got["problem"] != "First\n\nSecond" {
		t.Fatalf("problem = %q, want multiline value", got["problem"])
	}
	if got["context"] != "a=b" {
		t.Fatalf("context = %q, want value with equals", got["context"])
	}
}

func TestWorkflowStepTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		step formula.Step
		want string
	}{
		{name: "default rig", step: formula.Step{}, want: "gastown"},
		{name: "explicit rig", step: formula.Step{Target: "rig"}, want: "gastown"},
		{name: "mayor", step: formula.Step{Target: "mayor"}, want: "mayor"},
		{name: "crew path", step: formula.Step{Target: "gastown/crew/alex"}, want: "gastown/crew/alex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := workflowStepTarget(tt.step, "gastown"); got != tt.want {
				t.Fatalf("workflowStepTarget() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWorkflowStepDescriptionAddsTargetMetadata(t *testing.T) {
	t.Parallel()

	description := "Line one\n\nLine two"
	got := workflowStepDescription(formula.Step{Target: "mayor"}, description)
	want := "workflow_target: mayor\n\nLine one\n\nLine two"
	if got != want {
		t.Fatalf("workflowStepDescription() = %q, want %q", got, want)
	}
}

func TestWorkflowStepTargetFromDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		description string
		want        string
	}{
		{name: "no metadata", description: "Body only", want: ""},
		{name: "mayor", description: "workflow_target: mayor\n\nBody", want: "mayor"},
		{name: "rig alias", description: "workflow_target: rig\n\nBody", want: "gastown"},
		{name: "path target", description: "workflow_target: gastown/crew/alex\n\nBody", want: "gastown/crew/alex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := workflowStepTargetFromDescription(tt.description, "gastown"); got != tt.want {
				t.Fatalf("workflowStepTargetFromDescription() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAttachmentFormulaVarsPrefersAttachedVars(t *testing.T) {
	t.Parallel()

	attachment := &beads.AttachmentFields{
		AttachedVars: []string{"problem=First\n\nSecond"},
		FormulaVars:  "problem=First\n\ntruncated",
	}
	got := attachmentFormulaVars(attachment)
	want := []string{"problem=First\n\nSecond"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attachmentFormulaVars() = %#v, want %#v", got, want)
	}
}
