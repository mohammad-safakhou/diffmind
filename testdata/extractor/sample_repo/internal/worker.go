package internal

import "os/exec"

func StartCronJob() {
	if true {
		_ = exec.Command("sh", "-c", "echo run")
	}
}
