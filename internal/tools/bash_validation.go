package tools

import (
	"fmt"
	"strings"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// CommandIntent classifies the semantic purpose of a bash command. Ported
// from claw-code's bash_validation.rs (CommandIntent enum). Used by
// ClassifyCommand — orthogonal to the Block/Warn decision made by
// ValidateCommand.
type CommandIntent int

const (
	IntentUnknown CommandIntent = iota
	IntentReadOnly
	IntentWrite
	IntentDestructive
	IntentNetwork
	IntentProcessManagement
	IntentPackageManagement
	IntentSystemAdmin
)

// String returns a short debug label.
func (c CommandIntent) String() string {
	switch c {
	case IntentReadOnly:
		return "read_only"
	case IntentWrite:
		return "write"
	case IntentDestructive:
		return "destructive"
	case IntentNetwork:
		return "network"
	case IntentProcessManagement:
		return "process"
	case IntentPackageManagement:
		return "package"
	case IntentSystemAdmin:
		return "system_admin"
	default:
		return "unknown"
	}
}

// ValidationKind is the verdict produced by ValidateCommand.
type ValidationKind int

const (
	// ValidationAllow: command is safe to run under the current mode.
	ValidationAllow ValidationKind = iota
	// ValidationBlock: command must be refused; include Reason in the error.
	ValidationBlock
	// ValidationWarn: command is runnable but the caller should surface the
	// Message to the user. Phase 3 C3 treats this as "run, but prefix output
	// with the warning"; future UI layers may promote it to interactive
	// confirmation.
	ValidationWarn
)

// ValidationResult is the typed verdict. Reason is populated for Block,
// Message for Warn, both empty for Allow.
type ValidationResult struct {
	Kind    ValidationKind
	Reason  string
	Message string
}

// Allow returns the zero-value Allow verdict.
func Allow() ValidationResult { return ValidationResult{Kind: ValidationAllow} }

// Block returns a Block verdict with the given reason.
func Block(reason string) ValidationResult {
	return ValidationResult{Kind: ValidationBlock, Reason: reason}
}

// Warn returns a Warn verdict with the given message.
func Warn(message string) ValidationResult {
	return ValidationResult{Kind: ValidationWarn, Message: message}
}

// ---------------------------------------------------------------------------
// Command-class lists.
// ---------------------------------------------------------------------------
//
// These mirror claw-code/bash_validation.rs. Kept as map[string]struct{}
// for O(1) lookup. If drift from upstream matters, add a test that
// enumerates both.

var writeCommands = stringSet(
	"cp", "mv", "rm", "mkdir", "rmdir", "touch", "chmod", "chown", "chgrp",
	"ln", "install", "tee", "truncate", "shred", "mkfifo", "mknod", "dd",
)

var stateModifyingCommands = stringSet(
	"apt", "apt-get", "yum", "dnf", "pacman", "brew", "pip", "pip3", "npm",
	"yarn", "pnpm", "bun", "cargo", "gem", "go", "rustup", "docker",
	"systemctl", "service", "mount", "umount", "kill", "pkill", "killall",
	"reboot", "shutdown", "halt", "poweroff", "useradd", "userdel", "usermod",
	"groupadd", "groupdel", "crontab", "at",
)

var readOnlyCommands = stringSet(
	"ls", "cat", "head", "tail", "less", "more", "wc", "sort", "uniq",
	"grep", "egrep", "fgrep", "find", "which", "whereis", "whatis", "man",
	"info", "file", "stat", "du", "df", "free", "uptime", "uname",
	"hostname", "whoami", "id", "groups", "env", "printenv", "echo",
	"printf", "date", "cal", "bc", "expr", "test", "true", "false", "pwd",
	"tree", "diff", "cmp", "md5sum", "sha256sum", "sha1sum", "xxd", "od",
	"hexdump", "strings", "readlink", "realpath", "basename", "dirname",
	"seq", "yes", "tput", "column", "jq", "yq", "xargs", "tr", "cut",
	"paste", "awk", "sed",
)

var networkCommands = stringSet(
	"curl", "wget", "ssh", "scp", "rsync", "ftp", "sftp", "nc", "ncat",
	"telnet", "ping", "traceroute", "dig", "nslookup", "host", "whois",
	"ifconfig", "ip", "netstat", "ss", "nmap",
)

var processCommands = stringSet(
	"kill", "pkill", "killall", "ps", "top", "htop", "bg", "fg", "jobs",
	"nohup", "disown", "wait", "nice", "renice",
)

var packageCommands = stringSet(
	"apt", "apt-get", "yum", "dnf", "pacman", "brew", "pip", "pip3", "npm",
	"yarn", "pnpm", "bun", "cargo", "gem", "go", "rustup", "snap", "flatpak",
)

var systemAdminCommands = stringSet(
	"sudo", "su", "chroot", "mount", "umount", "fdisk", "parted", "lsblk",
	"blkid", "systemctl", "service", "journalctl", "dmesg", "modprobe",
	"insmod", "rmmod", "iptables", "ufw", "firewall-cmd", "sysctl",
	"crontab", "at", "useradd", "userdel", "usermod", "groupadd", "groupdel",
	"passwd", "visudo",
)

var alwaysDestructiveCommands = stringSet("shred", "wipefs")

var gitReadOnlySubcommands = stringSet(
	"status", "log", "diff", "show", "branch", "tag", "stash", "remote",
	"fetch", "ls-files", "ls-tree", "cat-file", "rev-parse", "describe",
	"shortlog", "blame", "bisect", "reflog", "config",
)

var writeRedirections = []string{">", ">>", ">&"}

// destructivePattern is a literal substring → human-readable warning.
type destructivePattern struct {
	Pattern string
	Warning string
}

// destructivePatterns: substring-safe destructive-command warnings.
//
// The `rm -rf <target>` variants (root /, home ~, wildcard *, current .)
// were removed because substring matching produces false positives
// (`rm -rf /tmp/x` matching "rm -rf /"). rmHasRecursiveForce() is the
// single source of truth for "any rm -rf" warnings; BLOCKED_PATTERNS
// hard-blocks the literal-root case.
var destructivePatterns = []destructivePattern{
	{"mkfs", "Filesystem creation will destroy existing data on the device"},
	{"dd if=", "Direct disk write — can overwrite partitions or devices"},
	{"> /dev/sd", "Writing to raw disk device"},
	{"chmod -R 777", "Recursively setting world-writable permissions"},
	{"chmod -R 000", "Recursively removing all permissions"},
	{":(){ :|:& };:", "Fork bomb — will crash the system"},
}

// systemPaths are absolute prefixes outside a normal user workspace.
var systemPaths = []string{
	"/etc/", "/usr/", "/var/", "/boot/", "/sys/", "/proc/", "/dev/",
	"/sbin/", "/lib/", "/opt/",
}

func stringSet(names ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

// ---------------------------------------------------------------------------
// Pipeline entry point.
// ---------------------------------------------------------------------------

// ValidateCommand runs the full validation pipeline and returns the first
// non-Allow verdict (or Allow if every stage passes).
//
// Stages (order matters — Block > Warn > Allow):
//  1. validateMode — mode-level rules (read-only write/state block, workspace-write system-path warn)
//  2. validateSed — sed -i in read-only
//  3. checkDestructive — known-destructive patterns (warn)
//  4. validatePaths — traversal / $HOME escape heuristics (warn)
//
// workspace is the resolved absolute path of the workspace root — used by
// validatePaths to allow traversals that resolve inside the workspace.
// Pass "" if unknown.
func ValidateCommand(command string, mode runtime.PermissionMode, workspace string) ValidationResult {
	if r := validateMode(command, mode); r.Kind != ValidationAllow {
		return r
	}
	if r := validateSed(command, mode); r.Kind != ValidationAllow {
		return r
	}
	if r := checkDestructive(command); r.Kind != ValidationAllow {
		return r
	}
	return validatePaths(command, workspace)
}

// ClassifyCommand returns the semantic intent of a command. Pure
// classification — never returns Block/Warn.
func ClassifyCommand(command string) CommandIntent {
	first := extractFirstCommand(command)
	if first == "" {
		return IntentUnknown
	}

	if _, ok := readOnlyCommands[first]; ok {
		// sed -i is a write in disguise.
		if first == "sed" && strings.Contains(command, " -i") {
			return IntentWrite
		}
		return IntentReadOnly
	}
	if _, ok := alwaysDestructiveCommands[first]; ok {
		return IntentDestructive
	}
	if first == "rm" {
		return IntentDestructive
	}
	if _, ok := writeCommands[first]; ok {
		return IntentWrite
	}
	if _, ok := networkCommands[first]; ok {
		return IntentNetwork
	}
	if _, ok := processCommands[first]; ok {
		return IntentProcessManagement
	}
	if _, ok := packageCommands[first]; ok {
		return IntentPackageManagement
	}
	if _, ok := systemAdminCommands[first]; ok {
		return IntentSystemAdmin
	}
	if first == "git" {
		return classifyGitCommand(command)
	}
	return IntentUnknown
}

func classifyGitCommand(command string) CommandIntent {
	sub := gitSubcommand(command)
	if sub == "" {
		return IntentReadOnly
	}
	if _, ok := gitReadOnlySubcommands[sub]; ok {
		return IntentReadOnly
	}
	return IntentWrite
}

// ---------------------------------------------------------------------------
// Stage: validateMode.
// ---------------------------------------------------------------------------

func validateMode(command string, mode runtime.PermissionMode) ValidationResult {
	switch mode {
	case runtime.ModeReadOnly:
		return validateReadOnly(command)
	case runtime.ModeWorkspaceWrite:
		if commandTargetsOutsideWorkspace(command) {
			return Warn("Command targets a system path outside the workspace — requires elevated permission")
		}
		return Allow()
	case runtime.ModeDangerFullAccess:
		return Allow()
	default:
		return Allow()
	}
}

func validateReadOnly(command string) ValidationResult {
	first := extractFirstCommand(command)
	if first == "" {
		return Allow()
	}

	if _, ok := writeCommands[first]; ok {
		return Block(fmt.Sprintf("Command %q modifies the filesystem and is not allowed in read-only mode", first))
	}
	if _, ok := stateModifyingCommands[first]; ok {
		return Block(fmt.Sprintf("Command %q modifies system state and is not allowed in read-only mode", first))
	}

	// sudo wraps something else — recurse.
	if first == "sudo" {
		inner := extractSudoInner(command)
		if inner != "" {
			if r := validateReadOnly(inner); r.Kind != ValidationAllow {
				return r
			}
		}
	}

	for _, redir := range writeRedirections {
		if strings.Contains(command, redir) {
			return Block(fmt.Sprintf("Command contains write redirection %q which is not allowed in read-only mode", redir))
		}
	}

	if first == "git" {
		return validateGitReadOnly(command)
	}
	return Allow()
}

func validateGitReadOnly(command string) ValidationResult {
	sub := gitSubcommand(command)
	if sub == "" {
		return Allow()
	}
	if _, ok := gitReadOnlySubcommands[sub]; ok {
		return Allow()
	}
	return Block(fmt.Sprintf("Git subcommand %q modifies repository state and is not allowed in read-only mode", sub))
}

func commandTargetsOutsideWorkspace(command string) bool {
	first := extractFirstCommand(command)
	_, isWrite := writeCommands[first]
	_, isState := stateModifyingCommands[first]
	if !isWrite && !isState {
		return false
	}
	for _, sp := range systemPaths {
		if strings.Contains(command, sp) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Stage: validateSed.
// ---------------------------------------------------------------------------

func validateSed(command string, mode runtime.PermissionMode) ValidationResult {
	if extractFirstCommand(command) != "sed" {
		return Allow()
	}
	if mode == runtime.ModeReadOnly && strings.Contains(command, " -i") {
		return Block("sed -i (in-place editing) is not allowed in read-only mode")
	}
	return Allow()
}

// ---------------------------------------------------------------------------
// Stage: checkDestructive.
// ---------------------------------------------------------------------------

func checkDestructive(command string) ValidationResult {
	for _, dp := range destructivePatterns {
		if strings.Contains(command, dp.Pattern) {
			return Warn("Destructive command detected: " + dp.Warning)
		}
	}
	first := extractFirstCommand(command)
	if first != "" {
		if _, ok := alwaysDestructiveCommands[first]; ok {
			return Warn(fmt.Sprintf("Command %q is inherently destructive and may cause data loss", first))
		}
	}
	// Catch-all rm -rf (any flag form). We scan the full command so
	// compound commands like `mkdir x && rm -rf x` are still caught.
	if rmHasRecursiveForce(command) {
		return Warn("Recursive forced deletion detected — verify the target path is correct")
	}
	return Allow()
}

// rmHasRecursiveForce returns true when the command both contains an `rm`
// token AND a recursive + force flag combination. Checking an `rm` token
// rules out unrelated programs that happen to accept `-r -f`.
func rmHasRecursiveForce(command string) bool {
	hasRm := false
	recursive, force := false, false
	for _, tok := range strings.Fields(command) {
		if tok == "rm" {
			hasRm = true
			continue
		}
		if tok == "--recursive" {
			recursive = true
			continue
		}
		if tok == "--force" {
			force = true
			continue
		}
		if strings.HasPrefix(tok, "--") || !strings.HasPrefix(tok, "-") {
			continue
		}
		// Short-flag cluster (-rf, -Rf, -fr, -r, -f, ...).
		for _, ch := range tok[1:] {
			switch ch {
			case 'r', 'R':
				recursive = true
			case 'f':
				force = true
			}
		}
	}
	return hasRm && recursive && force
}

// ---------------------------------------------------------------------------
// Stage: validatePaths.
// ---------------------------------------------------------------------------

func validatePaths(command, workspace string) ValidationResult {
	if strings.Contains(command, "../") {
		if workspace == "" || !strings.Contains(command, workspace) {
			return Warn("Command contains directory traversal pattern '../' — verify the target path resolves within the workspace")
		}
	}
	if strings.Contains(command, "~/") || strings.Contains(command, "$HOME") {
		return Warn("Command references home directory — verify it stays within the workspace scope")
	}
	return Allow()
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// extractFirstCommand strips leading environment-variable assignments
// (`KEY=val cmd ...`) and returns the first bare token. Returns "" for
// all-env-var inputs, empty commands, or whitespace only.
func extractFirstCommand(command string) string {
	remaining := strings.TrimSpace(command)
	for {
		trimmed := strings.TrimLeft(remaining, " \t")
		eqIdx := strings.IndexByte(trimmed, '=')
		if eqIdx <= 0 {
			break
		}
		name := trimmed[:eqIdx]
		if !isEnvVarName(name) {
			break
		}
		// Skip past the value. Find where the current KEY=value ends.
		afterEq := trimmed[eqIdx+1:]
		endOfValue := findEndOfValue(afterEq)
		if endOfValue < 0 {
			return ""
		}
		remaining = afterEq[endOfValue:]
	}
	fields := strings.Fields(remaining)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// isEnvVarName returns true for names made of [A-Za-z0-9_].
func isEnvVarName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r == '_' ||
			(r >= '0' && r <= '9') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= 'a' && r <= 'z')) {
			return false
		}
	}
	return true
}

// findEndOfValue returns the offset (into s) at which the current token
// ends, handling simple quoting. Returns -1 if the token runs to EOL and
// there is no command after it.
func findEndOfValue(s string) int {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= len(s) {
		return -1
	}
	if s[i] == '"' || s[i] == '\'' {
		quote := s[i]
		i++
		for i < len(s) {
			if s[i] == quote && (i == 0 || s[i-1] != '\\') {
				i++
				break
			}
			i++
		}
	} else {
		for i < len(s) && s[i] != ' ' && s[i] != '\t' {
			i++
		}
	}
	// We now want to move past any trailing whitespace until the next
	// non-whitespace token begins (that's where the command proper starts).
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= len(s) {
		return -1
	}
	return i
}

// sudoValueFlags are short flags that take a value as the next token. We
// must skip both the flag AND its value so the returned "inner" command
// actually points at the wrapped command, not the flag argument.
var sudoValueFlags = stringSet(
	"-u", "-g", "-U", "-C", "-r", "-t", "-T", "-D", "-p", "-h",
)

// extractSudoInner returns the command text after "sudo" and any sudo flags
// (including value-taking flags like "-u user"). Returns "" when no inner
// command is present.
func extractSudoInner(command string) string {
	fields := strings.Fields(command)
	sudoIdx := -1
	for i, f := range fields {
		if f == "sudo" {
			sudoIdx = i
			break
		}
	}
	if sudoIdx < 0 {
		return ""
	}
	rest := fields[sudoIdx+1:]
	i := 0
	for i < len(rest) {
		part := rest[i]
		if part == "--" {
			i++
			break
		}
		if !strings.HasPrefix(part, "-") {
			break
		}
		if _, takesValue := sudoValueFlags[part]; takesValue {
			i += 2 // flag + value
			continue
		}
		i++ // plain flag cluster
	}
	if i >= len(rest) {
		return ""
	}
	innerFirst := rest[i]
	// Locate innerFirst in the original command to preserve spacing/quotes.
	if off := strings.Index(command, innerFirst); off >= 0 {
		return command[off:]
	}
	return strings.Join(rest[i:], " ")
}

// gitValueFlags are the short git flags (before the subcommand) that take
// a value as the next token, e.g. `git -C /path diff`.
var gitValueFlags = stringSet("-C", "-c")

// gitSubcommand returns the first non-flag token after "git" (skipping
// "-C /path", "-c key=value", "--git-dir=...", etc.). Returns "" for bare
// "git" or when only flags are present.
func gitSubcommand(command string) string {
	fields := strings.Fields(command)
	seenGit := false
	i := 0
	for i < len(fields) {
		f := fields[i]
		if !seenGit {
			if f == "git" {
				seenGit = true
			}
			i++
			continue
		}
		if !strings.HasPrefix(f, "-") {
			return f
		}
		if _, takesValue := gitValueFlags[f]; takesValue {
			i += 2 // flag + value
			continue
		}
		i++ // plain flag (e.g. --no-pager, --git-dir=/path)
	}
	return ""
}
