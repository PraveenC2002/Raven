package raven

import (
	"testing"
)

func TestSSHBouncer(t *testing.T) {
	b := &sshBouncer{Policy: defaultShellPolicy}

	// deny list
	denyCases := []string{
		"/etc/shadow",
		"/home/user/.ssh/id_rsa",
		"/app/.env",
		"/proc/self/environ",
	}
	for _, v := range denyCases {
		if err := b.checkDenyList(v); err == nil {
			t.Errorf("checkDenyList(%q): expected denial, got nil", v)
		}
	}
	if err := b.checkDenyList("/var/log/syslog"); err != nil {
		t.Errorf("checkDenyList(/var/log/syslog): expected allow, got %v", err)
	}

	// validate: unknown command
	if err := b.validate(&remoteSSHFunctionCall{Command: "rm"}); err == nil {
		t.Errorf("validate(rm): expected error, got nil")
	}

	// validate: missing required positional
	if err := b.validate(&remoteSSHFunctionCall{Command: "cat"}); err == nil {
		t.Errorf("validate(cat with no positional): expected error, got nil")
	}

	// validate: unknown flag
	catUnknownFlag := &remoteSSHFunctionCall{
		Command: "cat",
		Flags: []*shellFlag{
			{Name: "-z"},
		},
		Positionals: []*shellPositional{
			{Index: 0, Value: "/var/log/syslog"},
		},
	}
	if err := b.validate(catUnknownFlag); err == nil {
		t.Errorf("validate(cat -z): expected error for unknown flag, got nil")
	}

	// validate + constructCmd: cat /var/log/syslog (positional single-quoted)
	catOK := &remoteSSHFunctionCall{
		Command: "cat",
		Positionals: []*shellPositional{
			{Index: 0, Value: "/var/log/syslog"},
		},
	}
	if err := b.validate(catOK); err != nil {
		t.Fatalf("validate(cat /var/log/syslog): unexpected error: %v", err)
	}
	got, err := b.constructCmd(catOK)
	if err != nil {
		t.Fatalf("constructCmd(cat /var/log/syslog): unexpected error: %v", err)
	}
	if want := "cat '/var/log/syslog'"; got != want {
		t.Errorf("constructCmd(cat) = %q, want %q", got, want)
	}

	// validate: deny-listed positional rejected even though it matches the accept pattern
	catDenied := &remoteSSHFunctionCall{
		Command: "cat",
		Positionals: []*shellPositional{
			{Index: 0, Value: "/etc/shadow"},
		},
	}
	if err := b.validate(catDenied); err == nil {
		t.Errorf("validate(cat /etc/shadow): expected deny-list error, got nil")
	}

	// validate + constructCmd: head -n 20 /var/log/syslog (spaced value flag, both quoted)
	headOK := &remoteSSHFunctionCall{
		Command: "head",
		Flags: []*shellFlag{
			{Name: "-n", Value: "20"},
		},
		Positionals: []*shellPositional{
			{Index: 0, Value: "/var/log/syslog"},
		},
	}
	if err := b.validate(headOK); err != nil {
		t.Fatalf("validate(head -n 20 ...): unexpected error: %v", err)
	}
	got, err = b.constructCmd(headOK)
	if err != nil {
		t.Fatalf("constructCmd(head): unexpected error: %v", err)
	}
	if want := "head -n '20' '/var/log/syslog'"; got != want {
		t.Errorf("constructCmd(head) = %q, want %q", got, want)
	}

	// validate: head -n with non-numeric value rejected
	headBadVal := &remoteSSHFunctionCall{
		Command: "head",
		Flags: []*shellFlag{
			{Name: "-n", Value: "abc"},
		},
		Positionals: []*shellPositional{
			{Index: 0, Value: "/var/log/syslog"},
		},
	}
	if err := b.validate(headBadVal); err == nil {
		t.Errorf("validate(head -n abc): expected error, got nil")
	}

	// validate + constructCmd: ps --sort=-pid (glued value flag, quoted)
	psOK := &remoteSSHFunctionCall{
		Command: "ps",
		Flags: []*shellFlag{
			{Name: "--sort", Value: "-pid"},
		},
	}
	if err := b.validate(psOK); err != nil {
		t.Fatalf("validate(ps --sort=-pid): unexpected error: %v", err)
	}
	got, err = b.constructCmd(psOK)
	if err != nil {
		t.Fatalf("constructCmd(ps): unexpected error: %v", err)
	}
	if want := "ps --sort='-pid'"; got != want {
		t.Errorf("constructCmd(ps) = %q, want %q", got, want)
	}

	// validate + constructCmd: grep with two positionals (0-based indexing, both quoted)
	grepOK := &remoteSSHFunctionCall{
		Command: "grep",
		Flags: []*shellFlag{
			{Name: "-i"},
			{Name: "-r"},
		},
		Positionals: []*shellPositional{
			{Index: 0, Value: "ERROR"},
			{Index: 1, Value: "/var/log/app"},
		},
	}
	if err := b.validate(grepOK); err != nil {
		t.Fatalf("validate(grep): unexpected error: %v", err)
	}
	got, err = b.constructCmd(grepOK)
	if err != nil {
		t.Fatalf("constructCmd(grep): unexpected error: %v", err)
	}
	if want := "grep -i -r 'ERROR' '/var/log/app'"; got != want {
		t.Errorf("constructCmd(grep) = %q, want %q", got, want)
	}

	// validate: duplicate positional index rejected
	grepDup := &remoteSSHFunctionCall{
		Command: "grep",
		Positionals: []*shellPositional{
			{Index: 0, Value: "ERROR"},
			{Index: 0, Value: "WARN"},
			{Index: 1, Value: "/var/log/app"},
		},
	}
	if err := b.validate(grepDup); err == nil {
		t.Errorf("validate(grep dup index): expected error, got nil")
	}

	// validate + constructCmd: systemctl status sshd (positionals quoted)
	sysctlOK := &remoteSSHFunctionCall{
		Command: "systemctl",
		Positionals: []*shellPositional{
			{Index: 0, Value: "status"},
			{Index: 1, Value: "sshd"},
		},
	}
	if err := b.validate(sysctlOK); err != nil {
		t.Fatalf("validate(systemctl status sshd): unexpected error: %v", err)
	}
	got, err = b.constructCmd(sysctlOK)
	if err != nil {
		t.Fatalf("constructCmd(systemctl): unexpected error: %v", err)
	}
	if want := "systemctl --no-pager  'status' 'sshd'"; got != want {
		t.Errorf("constructCmd(systemctl) = %q, want %q", got, want)
	}

	// validate: systemctl restart rejected (not a read-only verb)
	sysctlBad := &remoteSSHFunctionCall{
		Command: "systemctl",
		Positionals: []*shellPositional{
			{Index: 0, Value: "restart"},
			{Index: 1, Value: "sshd"},
		},
	}
	if err := b.validate(sysctlBad); err == nil {
		t.Errorf("validate(systemctl restart): expected error, got nil")
	}
}
