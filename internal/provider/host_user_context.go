package provider

import (
	"fmt"
	osuser "os/user"
	"strings"
)

func currentHostUsername() (string, error) {
	current, err := osuser.Current()
	if err != nil {
		return "", fmt.Errorf("resolve current user: %w", err)
	}
	if strings.TrimSpace(current.Username) == "" {
		return "", fmt.Errorf("resolve current user: username is empty")
	}

	username := current.Username
	if before, after, ok := strings.Cut(username, "\\"); ok && before != "" && after != "" {
		username = after
	}

	return username, nil
}
