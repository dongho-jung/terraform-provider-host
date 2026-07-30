package provider

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

type HostDesktopSessionValidator interface {
	Validate(targetUser string) error
}

type hostDesktopSessionValidator struct {
	currentUsername func() (string, error)
	activeUsername  func() (string, error)
}

func NewHostDesktopSessionValidator() HostDesktopSessionValidator {
	return &hostDesktopSessionValidator{
		currentUsername: currentHostUsername,
		activeUsername:  activeHostDesktopUsername,
	}
}

func (v *hostDesktopSessionValidator) Validate(targetUser string) error {
	targetUser = strings.TrimSpace(targetUser)
	if targetUser == "" {
		return fmt.Errorf("provider target_user is not configured")
	}

	executionUser, err := v.currentUsername()
	if err != nil {
		return err
	}
	if executionUser != targetUser {
		return fmt.Errorf("terraform is running as local user %q, but this resource belongs to provider target_user %q", executionUser, targetUser)
	}

	activeUser, err := v.activeUsername()
	if err != nil {
		return err
	}
	if activeUser != targetUser {
		if activeUser == "" || activeUser == "root" || activeUser == "loginwindow" {
			return fmt.Errorf("no macOS desktop session is active for provider target_user %q", targetUser)
		}
		return fmt.Errorf("the active macOS desktop session belongs to %q, but this resource belongs to provider target_user %q", activeUser, targetUser)
	}

	return nil
}

func requireHostDesktopSession(targetUser string, validator HostDesktopSessionValidator, diagnostics *diag.Diagnostics) bool {
	if validator == nil {
		return true
	}
	if err := validator.Validate(targetUser); err != nil {
		diagnostics.AddError(
			"Active target-user macOS desktop session required",
			fmt.Sprintf(
				"Session validation failed: %s. Log in to the macOS desktop as %q and run Terraform from a terminal in that login session; do not run Terraform through sudo. To apply specific unaffected resources first, run `terraform apply -target=<resource-address>` and repeat `-target` for each required address.",
				err,
				targetUser,
			),
		)
		return false
	}
	return true
}
