package provider

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestMacOSDockFileURL(t *testing.T) {
	t.Parallel()

	got := macOSDockFileURL("/Applications/Google Chrome.app")
	want := "file:///Applications/Google%20Chrome.app/"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseMacOSDockFileURLs(t *testing.T) {
	t.Parallel()

	out := `(
        {
        "tile-data" =         {
            "file-data" =             {
                "_CFURLString" = "file:///System/Applications/System%20Settings.app/";
                "_CFURLStringType" = 15;
            };
        };
    },
        {
        "tile-data" =         {
            "file-data" =             {
                "_CFURLString" = "file:///Applications/Google%20Chrome.app/";
                "_CFURLStringType" = 15;
            };
        };
    }
)`

	got := parseMacOSDockFileURLs(out)
	want := []string{"/System/Applications/System Settings.app", "/Applications/Google Chrome.app"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestMacOSDockEntry(t *testing.T) {
	t.Parallel()

	got := macOSDockEntry("/Applications/Google Chrome.app", "file-tile")
	if !strings.Contains(got, `"tile-type"="file-tile"`) {
		t.Fatalf("entry missing tile type: %s", got)
	}
	if !strings.Contains(got, `"_CFURLString"="file:///Applications/Google%20Chrome.app/"`) {
		t.Fatalf("entry missing URL: %s", got)
	}
	if !strings.Contains(got, `"file-label"="Google Chrome"`) {
		t.Fatalf("entry missing label: %s", got)
	}
}

func TestCLIMacOSDockManagerWriteDock(t *testing.T) {
	t.Parallel()

	var calls []string
	manager := &CLIMacOSDockManager{
		defaultsPath: "defaults",
		run: func(ctx context.Context, command string, args ...string) ([]byte, error) {
			calls = append(calls, command+" "+strings.Join(args, " "))
			return nil, nil
		},
	}

	err := manager.WriteDock(t.Context(), MacOSDockSpec{
		Apps:    []string{"/Applications/Google Chrome.app"},
		Folders: []string{"/Users/dongho/Downloads"},
	})
	if err != nil {
		t.Fatalf("WriteDock: %s", err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls got %#v", calls)
	}
	if !strings.Contains(calls[0], "write com.apple.dock persistent-apps -array") {
		t.Fatalf("apps command got %q", calls[0])
	}
	if !strings.Contains(calls[0], "file:///Applications/Google%20Chrome.app/") {
		t.Fatalf("apps command missing app URL: %q", calls[0])
	}
	if !strings.Contains(calls[1], "write com.apple.dock persistent-others -array") {
		t.Fatalf("folders command got %q", calls[1])
	}
	if !strings.Contains(calls[1], "file:///Users/dongho/Downloads/") {
		t.Fatalf("folders command missing folder URL: %q", calls[1])
	}
}

func TestMacOSDockManagedStateSortsByPriority(t *testing.T) {
	t.Parallel()

	spec, err := macOSDockSpecFromManagedState(macOSDockManagedState{
		Apps: map[string]macOSDockManagedItemState{
			"chrome": {
				Path:     "/Applications/Google Chrome.app",
				Priority: 20,
			},
			"settings": {
				Path:     "/System/Applications/System Settings.app",
				Priority: 10,
			},
		},
		Folders: map[string]macOSDockManagedItemState{
			"downloads": {
				Path:     "/Users/dongho/Downloads",
				Priority: 10,
			},
		},
	})
	if err != nil {
		t.Fatalf("macOSDockSpecFromManagedState: %s", err)
	}

	wantApps := []string{"/System/Applications/System Settings.app", "/Applications/Google Chrome.app"}
	if !reflect.DeepEqual(spec.Apps, wantApps) {
		t.Fatalf("apps got %#v, want %#v", spec.Apps, wantApps)
	}
	wantFolders := []string{"/Users/dongho/Downloads"}
	if !reflect.DeepEqual(spec.Folders, wantFolders) {
		t.Fatalf("folders got %#v, want %#v", spec.Folders, wantFolders)
	}
}

func TestMacOSDockManagedStateRejectsDuplicatePriority(t *testing.T) {
	t.Parallel()

	state := emptyMacOSDockManagedState()
	err := upsertMacOSDockManagedStateItem(&state, macOSDockManagedItemSpec{
		ID:       "hdi-11111111111111111111111111111111",
		Kind:     macOSDockItemKindApp,
		Path:     "/System/Applications/System Settings.app",
		Priority: 10,
	})
	if err != nil {
		t.Fatalf("first upsert: %s", err)
	}

	err = upsertMacOSDockManagedStateItem(&state, macOSDockManagedItemSpec{
		ID:       "hdi-22222222222222222222222222222222",
		Kind:     macOSDockItemKindApp,
		Path:     "/Applications/Google Chrome.app",
		Priority: 10,
	})
	if err == nil {
		t.Fatal("expected duplicate priority error")
	}
	if !strings.Contains(err.Error(), "priority 10") {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestMacOSDockManagedStateRejectsDuplicatePath(t *testing.T) {
	t.Parallel()

	state := emptyMacOSDockManagedState()
	err := upsertMacOSDockManagedStateItem(&state, macOSDockManagedItemSpec{
		ID:       "hdi-11111111111111111111111111111111",
		Kind:     macOSDockItemKindApp,
		Path:     "/Applications/Google Chrome.app",
		Priority: 10,
	})
	if err != nil {
		t.Fatalf("first upsert: %s", err)
	}

	err = upsertMacOSDockManagedStateItem(&state, macOSDockManagedItemSpec{
		ID:       "hdi-22222222222222222222222222222222",
		Kind:     macOSDockItemKindApp,
		Path:     "/Applications/Google Chrome.app",
		Priority: 20,
	})
	if err == nil {
		t.Fatal("expected duplicate path error")
	}
	if !strings.Contains(err.Error(), "already managed") {
		t.Fatalf("unexpected error: %s", err)
	}
}
