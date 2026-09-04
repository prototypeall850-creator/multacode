package permission

import "testing"

func TestPolicyForAgent(t *testing.T) {
	if got := PolicyForAgent("plan").Edit; got != Deny {
		t.Fatalf("plan edit = %s", got)
	}
	if got := PolicyForAgent("build").Edit; got != Ask {
		t.Fatalf("build edit = %s", got)
	}
}

func TestClassifyShell(t *testing.T) {
	if got := ClassifyShell("rm -rf /", Ask); got != Deny {
		t.Fatalf("destructive = %s", got)
	}
	if got := ClassifyShell("ls -la", Ask); got != Ask {
		t.Fatalf("readonly = %s", got)
	}
}
