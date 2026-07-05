package runtime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/fibegg/fibe-distilled/internal/optfibe"
)

// validateComposeProject rejects unsafe Docker Compose project names.
func validateComposeProject(project string) error {
	project = strings.TrimSpace(project)
	if project == "" {
		return errors.New("compose project is required")
	}
	if startsWithComposeSeparator(project) || strings.IndexFunc(project, invalidComposeProjectRune) >= 0 {
		return fmt.Errorf("unsafe compose project %q", project)
	}
	return nil
}

// startsWithComposeSeparator rejects names Compose may parse as flags/odd paths.
func startsWithComposeSeparator(project string) bool {
	return strings.HasPrefix(project, "-") || strings.HasPrefix(project, "_")
}

// invalidComposeProjectRune reports runes outside fibe-distilled's project subset.
func invalidComposeProjectRune(r rune) bool {
	return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_'
}

// playgroundBase validates a project and returns its /opt/fibe runtime path.
func playgroundBase(project string) (string, error) {
	if err := validateComposeProject(project); err != nil {
		return "", err
	}
	return optfibe.PlaygroundPath(project), nil
}
