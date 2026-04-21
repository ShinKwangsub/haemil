package tools

import (
	"strings"
	"testing"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

func TestExtractFirstCommand(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"ls", "ls"},
		{"ls -la", "ls"},
		{"  ls -la  ", "ls"},
		{"", ""},
		{"FOO=bar ls -la", "ls"},
		{"FOO=bar BAZ=qux ls", "ls"},
		{`FOO="bar baz" ls`, "ls"},
		{`FOO='single quoted' grep pattern`, "grep"},
		{"KEY=value", ""}, // no command, only env var
		{"=value cmd", "=value"},
		{"sudo rm -rf /", "sudo"},
		{"echo hello", "echo"},
	}
	for _, c := range cases {
		got := extractFirstCommand(c.in)
		if got != c.want {
			t.Errorf("extractFirstCommand(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtractSudoInner(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"sudo rm -rf /", "rm -rf /"},
		{"sudo -u root rm foo", "rm foo"},
		{"sudo -E -u user apt update", "apt update"},
		{"sudo", ""},
		{"sudo --", ""},
		{"rm foo", ""}, // no sudo
	}
	for _, c := range cases {
		got := extractSudoInner(c.in)
		if got != c.want {
			t.Errorf("extractSudoInner(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGitSubcommand(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"git status", "status"},
		{"git log --oneline", "log"},
		{"git -C /path diff", "diff"},
		{"git --no-pager log", "log"},
		{"git", ""},
		{"not-git foo", "foo"}, // fallthrough: first non-flag after seeing "git" token
	}
	for _, c := range cases {
		got := gitSubcommand(c.in)
		if c.in == "not-git foo" {
			// If "git" never appeared, gitSubcommand returns "". Verify.
			if got != "" {
				t.Errorf("gitSubcommand(%q): got %q, want \"\"", c.in, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("gitSubcommand(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}

// TestClassifyCommand checks the semantic intent classifier across every
// category.
func TestClassifyCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want CommandIntent
	}{
		{"ls -la", IntentReadOnly},
		{"cat /etc/hosts", IntentReadOnly},
		{"grep foo bar.txt", IntentReadOnly},
		{"sed 's/foo/bar/g' input.txt", IntentReadOnly},
		{"sed -i 's/foo/bar/g' input.txt", IntentWrite}, // -i makes it a write
		{"cp a b", IntentWrite},
		{"mv a b", IntentWrite},
		{"rm foo", IntentDestructive},
		{"shred file", IntentDestructive},
		{"wipefs /dev/sda1", IntentDestructive},
		{"curl https://example.com", IntentNetwork},
		{"kill 1234", IntentProcessManagement},
		{"npm install", IntentPackageManagement},
		{"sudo mount /dev/sda1 /mnt", IntentSystemAdmin},
		{"git status", IntentReadOnly},
		{"git log --oneline", IntentReadOnly},
		{"git commit -m 'x'", IntentWrite},
		{"git push", IntentWrite},
		{"", IntentUnknown},
		{"nonsense_cmd", IntentUnknown},
	}
	for _, c := range cases {
		got := ClassifyCommand(c.cmd)
		if got != c.want {
			t.Errorf("ClassifyCommand(%q): got %s, want %s", c.cmd, got, c.want)
		}
	}
}

// TestValidateReadOnlyMode: in ReadOnly mode, write / state-modifying /
// redirection / sudo-wrapped writes / git mutators → Block.
func TestValidateReadOnlyMode(t *testing.T) {
	blocks := []string{
		"rm foo",
		"cp a b",
		"mv a b",
		"mkdir x",
		"touch file",
		"echo hi > out.txt",
		"cat file >> out.txt",
		"sudo rm file",
		"npm install",
		"git commit -m x",
		"git push",
	}
	for _, cmd := range blocks {
		r := ValidateCommand(cmd, runtime.ModeReadOnly, "")
		if r.Kind != ValidationBlock {
			t.Errorf("ValidateCommand(%q, readonly): got %v, want Block", cmd, r)
		}
	}

	// These should NOT be blocked in readonly (reads or info commands).
	allows := []string{
		"ls -la",
		"cat /etc/hosts",
		"grep foo bar.txt",
		"git status",
		"git log --oneline",
		"git diff",
	}
	for _, cmd := range allows {
		r := ValidateCommand(cmd, runtime.ModeReadOnly, "")
		if r.Kind == ValidationBlock {
			t.Errorf("ValidateCommand(%q, readonly): got Block (%s), want Allow/Warn", cmd, r.Reason)
		}
	}
}

// TestValidateSedInReadOnly: sed -i must block in readonly but be fine in
// workspace-write.
func TestValidateSedInReadOnly(t *testing.T) {
	r := ValidateCommand("sed -i 's/foo/bar/g' input.txt", runtime.ModeReadOnly, "")
	if r.Kind != ValidationBlock {
		t.Errorf("sed -i in readonly: got %v, want Block", r)
	}
	if !strings.Contains(strings.ToLower(r.Reason), "sed") {
		t.Errorf("sed -i reason should mention sed, got %q", r.Reason)
	}

	r = ValidateCommand("sed -i 's/foo/bar/g' input.txt", runtime.ModeWorkspaceWrite, "")
	if r.Kind == ValidationBlock {
		t.Errorf("sed -i in workspace-write: got Block (%s), want Allow/Warn", r.Reason)
	}
}

// TestCheckDestructive: classic destructive patterns produce Warn.
func TestCheckDestructive(t *testing.T) {
	warns := []string{
		"rm -rf /",
		"rm -rf ~",
		"rm -rf *",
		"mkfs.ext4 /dev/sda1",
		"dd if=/dev/zero of=/dev/sda",
		"chmod -R 777 /",
		":(){ :|:& };:",
		"shred sensitive.txt",
		"wipefs /dev/sda1",
		"rm -rf build/",                              // catch-all rm -rf warn
		"mkdir /tmp/x && rm -rf /tmp/x",              // compound pipeline
		"rm -r --force /tmp/x",                       // --force variant
		"rm --recursive --force /tmp/x",              // long-form variant
	}
	for _, cmd := range warns {
		// Use DangerFull mode so only checkDestructive fires.
		r := ValidateCommand(cmd, runtime.ModeDangerFullAccess, "")
		if r.Kind != ValidationWarn {
			t.Errorf("ValidateCommand(%q, dangerfull): got %v, want Warn", cmd, r)
		}
	}
}

// TestValidatePathsTraversal: ../ and $HOME / ~/ warn in workspace-write.
func TestValidatePathsTraversal(t *testing.T) {
	r := ValidateCommand("cat ../../etc/passwd", runtime.ModeDangerFullAccess, "/workspace")
	if r.Kind != ValidationWarn {
		t.Errorf("traversal: got %v, want Warn", r)
	}

	r = ValidateCommand("cat ~/secret", runtime.ModeDangerFullAccess, "")
	if r.Kind != ValidationWarn {
		t.Errorf("~/ path: got %v, want Warn", r)
	}

	r = ValidateCommand("echo $HOME", runtime.ModeDangerFullAccess, "")
	if r.Kind != ValidationWarn {
		t.Errorf("$HOME: got %v, want Warn", r)
	}

	// Plain command — no path issue.
	r = ValidateCommand("ls -la", runtime.ModeDangerFullAccess, "")
	if r.Kind != ValidationAllow {
		t.Errorf("plain ls: got %v, want Allow", r)
	}
}

// TestWorkspaceWriteSystemPaths: writes that target absolute system paths
// warn in workspace-write mode. Non-system-path writes are Allow.
func TestWorkspaceWriteSystemPaths(t *testing.T) {
	cases := []string{
		"rm /etc/hosts",
		"touch /usr/local/bin/evil",
		"cp thing /var/log/",
	}
	for _, cmd := range cases {
		r := ValidateCommand(cmd, runtime.ModeWorkspaceWrite, "")
		if r.Kind != ValidationWarn {
			t.Errorf("ValidateCommand(%q, workspace-write): got %v, want Warn", cmd, r)
		}
	}

	// Workspace-local write should be Allow.
	r := ValidateCommand("touch ./file.txt", runtime.ModeWorkspaceWrite, "")
	if r.Kind != ValidationAllow {
		t.Errorf("workspace-local touch: got %v, want Allow", r)
	}
}

// TestDangerFullAllows: destructive-pattern warns still fire in DangerFull,
// but nothing else blocks.
func TestDangerFullAllows(t *testing.T) {
	cases := []string{
		"rm file",
		"cp a b",
		"npm install",
		"git push",
		"sudo apt update",
	}
	for _, cmd := range cases {
		r := ValidateCommand(cmd, runtime.ModeDangerFullAccess, "")
		if r.Kind == ValidationBlock {
			t.Errorf("ValidateCommand(%q, dangerfull): got Block (%s), want Allow/Warn", cmd, r.Reason)
		}
	}
}

// TestPipelineStageOrdering: Block wins over Warn. A sed -i command in
// readonly triggers both a mode block and the sed-specific block — we
// expect the mode check (first in the pipeline) to report its reason.
func TestPipelineStageOrdering(t *testing.T) {
	// "rm -rf /" is both mode-blocked (rm is a write command) AND
	// destructive-warned. Mode stage runs first → Block.
	r := ValidateCommand("rm -rf /", runtime.ModeReadOnly, "")
	if r.Kind != ValidationBlock {
		t.Errorf("rm -rf / in readonly: got %v, want Block", r)
	}
	if !strings.Contains(r.Reason, "rm") {
		t.Errorf("block reason should mention rm, got %q", r.Reason)
	}
}

// TestGitReadOnlySubcommands: in readonly, each allow-listed git subcommand
// passes; anything else is blocked.
func TestGitReadOnlySubcommands(t *testing.T) {
	allows := []string{"status", "log", "diff", "show", "branch", "tag", "blame", "config"}
	blocks := []string{"commit", "push", "pull", "merge", "rebase", "reset", "checkout"}
	for _, sub := range allows {
		cmd := "git " + sub
		r := ValidateCommand(cmd, runtime.ModeReadOnly, "")
		if r.Kind == ValidationBlock {
			t.Errorf("%q in readonly: got Block (%s), want Allow", cmd, r.Reason)
		}
	}
	for _, sub := range blocks {
		cmd := "git " + sub
		r := ValidateCommand(cmd, runtime.ModeReadOnly, "")
		if r.Kind != ValidationBlock {
			t.Errorf("%q in readonly: got %v, want Block", cmd, r)
		}
	}
}
