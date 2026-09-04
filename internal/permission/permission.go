// Package permission implements allow/ask/deny decisions
// and shell risk classification per plan.md.
package permission

type Decision string

const (
	Allow Decision = "allow"
	Ask   Decision = "ask"
	Deny  Decision = "deny"
)

// Policy maps tool class -> default decision.
type Policy struct {
	Read   Decision
	Search Decision
	Edit   Decision
	Shell  Decision
	Delete Decision
}

func DefaultPolicy() Policy {
	return Policy{Read: Allow, Search: Allow, Edit: Ask, Shell: Ask, Delete: Ask}
}

// Agent overrides per plan.md.
func PolicyForAgent(agent string) Policy {
	p := DefaultPolicy()
	switch agent {
	case "plan":
		p.Edit = Deny
		p.Delete = Deny
	case "build":
		// defaults already ask; destructive shell narrowed by ClassifyShell
	}
	return p
}

// ClassifyShell refines the shell decision by command pattern.
func ClassifyShell(cmd string, base Decision) Decision {
	if isDestructive(cmd) {
		return Deny
	}
	if isReadOnly(cmd) {
		// Read-ish commands inherit base but never escalate to deny.
		if base == Deny {
			return Deny
		}
		return base
	}
	return base
}
