package permission

import "strings"

var readOnlyPrefixes = []string{
	"ls", "pwd", "git status", "git diff", "rg ", "cat ",
	"sed ", "head ", "tail ", "echo ", "go list",
}

var destructiveMarkers = []string{
	"rm -rf /", "rm -rf ~", "rm -rf $HOME", ":(){:|:&};:",
	"mkfs", "dd if=", "chmod -R 777 /", "chown -R ",
}

func isReadOnly(cmd string) bool {
	c := strings.TrimSpace(cmd)
	for _, p := range readOnlyPrefixes {
		if strings.HasPrefix(c, strings.TrimSpace(p)) {
			return true
		}
	}
	return false
}

func isDestructive(cmd string) bool {
	for _, m := range destructiveMarkers {
		if strings.Contains(cmd, m) {
			return true
		}
	}
	return false
}
