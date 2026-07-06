//go:build !unix

package runtime

import "os/exec"

// prepareLocalCommand applies platform-specific local command hardening.
func prepareLocalCommand(_ *exec.Cmd) {}
