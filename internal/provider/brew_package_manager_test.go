package provider

import "testing"

func TestParseBrewFormulaStatus(t *testing.T) {
	t.Parallel()

	status, err := parseBrewPackageStatus("bat", brewPackageTypeFormula, []byte(`{
  "formulae": [
    {
      "name": "bat",
      "full_name": "bat",
      "versions": { "stable": "0.26.1" },
      "installed": [
        { "version": "0.26.0", "installed_on_request": true }
      ],
      "linked_keg": "0.26.0",
      "outdated": true,
      "pinned": false
    }
  ],
  "casks": []
}`))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if status.Name != "bat" {
		t.Fatalf("expected name bat, got %q", status.Name)
	}
	if status.PackageType != brewPackageTypeFormula {
		t.Fatalf("expected formula package type, got %q", status.PackageType)
	}
	if !status.Installed {
		t.Fatal("expected installed formula")
	}
	if status.InstalledVersion != "0.26.0" {
		t.Fatalf("expected installed version 0.26.0, got %q", status.InstalledVersion)
	}
	if status.CandidateVersion != "0.26.1" {
		t.Fatalf("expected candidate version 0.26.1, got %q", status.CandidateVersion)
	}
	if status.UpgradeVersion != "0.26.1" {
		t.Fatalf("expected upgrade version 0.26.1, got %q", status.UpgradeVersion)
	}
	if !status.InstalledOnRequest {
		t.Fatal("expected formula to be installed on request")
	}
}

func TestParseBrewFormulaStatusNotInstalled(t *testing.T) {
	t.Parallel()

	status, err := parseBrewPackageStatus("git", brewPackageTypeFormula, []byte(`{
  "formulae": [
    {
      "name": "git",
      "full_name": "git",
      "versions": { "stable": "2.54.0" },
      "installed": [],
      "linked_keg": null,
      "outdated": false,
      "pinned": false
    }
  ],
  "casks": []
}`))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if status.Installed {
		t.Fatal("expected formula to be missing")
	}
	if status.CandidateVersion != "2.54.0" {
		t.Fatalf("expected candidate version 2.54.0, got %q", status.CandidateVersion)
	}
}

func TestParseBrewCaskStatus(t *testing.T) {
	t.Parallel()

	status, err := parseBrewPackageStatus("docker-desktop", brewPackageTypeCask, []byte(`{
  "formulae": [],
  "casks": [
    {
      "token": "docker-desktop",
      "full_token": "docker-desktop",
      "version": "4.79.0,230596",
      "installed": "4.71.0,225177",
      "outdated": true,
      "pinned": false,
      "auto_updates": true,
      "artifacts": [
        {
          "app": ["Docker.app"],
          "target": "/Applications/Docker.app"
        }
      ]
    }
  ]
}`))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if status.PackageType != brewPackageTypeCask {
		t.Fatalf("expected cask package type, got %q", status.PackageType)
	}
	if !status.Installed {
		t.Fatal("expected installed cask")
	}
	if status.InstalledVersion != "4.71.0,225177" {
		t.Fatalf("expected installed version, got %q", status.InstalledVersion)
	}
	if status.UpgradeVersion != "4.79.0,230596" {
		t.Fatalf("expected upgrade version, got %q", status.UpgradeVersion)
	}
	if !status.AutoUpdates {
		t.Fatal("expected auto-updating cask")
	}
	if len(status.AppPaths) != 1 || status.AppPaths[0] != "/Applications/Docker.app" {
		t.Fatalf("expected Docker app path, got %#v", status.AppPaths)
	}
}

func TestBrewCaskAppPathsIgnoresNonAppArtifacts(t *testing.T) {
	t.Parallel()

	status, err := parseBrewPackageStatus("hammerspoon", brewPackageTypeCask, []byte(`{
  "formulae": [],
  "casks": [
    {
      "token": "hammerspoon",
      "full_token": "hammerspoon",
      "version": "1.1.1",
      "installed": "1.1.1",
      "outdated": false,
      "pinned": false,
      "auto_updates": false,
      "artifacts": [
        { "uninstall": [{ "quit": "org.hammerspoon.Hammerspoon" }] },
        {
          "app": ["Hammerspoon.app"],
          "target": "/Applications/Hammerspoon.app"
        },
        {
          "binary": ["/Applications/Hammerspoon.app/Contents/Frameworks/hs/hs"],
          "target": "/opt/homebrew/bin/hs"
        }
      ]
    }
  ]
}`))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	want := []string{"/Applications/Hammerspoon.app"}
	if len(status.AppPaths) != len(want) || status.AppPaths[0] != want[0] {
		t.Fatalf("app paths got %#v, want %#v", status.AppPaths, want)
	}
}
