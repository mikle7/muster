package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const sessPrefix = "mstr-"

func tmuxSession(name string) string { return sessPrefix + name }

func tmuxBin() string {
	if t := os.Getenv("MUSTER_TMUX"); t != "" {
		return t
	}
	return "tmux"
}

// tmuxArgs lets tests isolate a server: MUSTER_TMUX_ARGS="-L sock -f /dev/null".
func tmuxCmd(args ...string) *exec.Cmd {
	var pre []string
	if extra := os.Getenv("MUSTER_TMUX_ARGS"); extra != "" {
		pre = strings.Fields(extra)
	}
	return exec.Command(tmuxBin(), append(pre, args...)...)
}

func tmuxRun(args ...string) (string, error) {
	out, err := tmuxCmd(args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func tmuxHasSession(sess string) bool {
	// exact match; has-session -t does prefix matching, so use = qualifier
	_, err := tmuxRun("has-session", "-t", "="+sess)
	return err == nil
}

// tmuxNewSession spawns a detached session running shellCmd in dir with env.
func tmuxNewSession(sess, dir, shellCmd string, env map[string]string) error {
	args := []string{"new-session", "-d", "-s", sess, "-c", dir}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, shellCmd)
	out, err := tmuxRun(args...)
	if err != nil {
		return fmt.Errorf("tmux new-session: %v: %s", err, out)
	}
	return nil
}

func tmuxKillSession(sess string) error {
	out, err := tmuxRun("kill-session", "-t", "="+sess)
	if err != nil {
		return fmt.Errorf("tmux kill-session: %v: %s", err, out)
	}
	return nil
}

// tmuxSendLine types text into the session's active pane and presses Enter.
// -l sends the text literally (no key-name interpretation). Target form
// "=name:" = exact session match resolved to its active pane (bare "=name"
// is rejected by send-keys even though has-session accepts it).
func tmuxSendLine(sess, text string) error {
	target := "=" + sess + ":"
	if out, err := tmuxRun("send-keys", "-t", target, "-l", text); err != nil {
		return fmt.Errorf("tmux send-keys: %v: %s", err, out)
	}
	// separate call so a literal "Enter" in text can't be swallowed
	if out, err := tmuxRun("send-keys", "-t", target, "Enter"); err != nil {
		return fmt.Errorf("tmux send-keys enter: %v: %s", err, out)
	}
	return nil
}

// shQuote single-quotes s for POSIX shells.
func shQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'\\$`!*?[](){};&|<>~#%^=") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shQuote(a)
	}
	return strings.Join(parts, " ")
}
