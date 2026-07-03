package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestResolveMacOSAudioDeviceSelectors(t *testing.T) {
	t.Parallel()

	devices := []MacOSAudioDevice{
		{
			UID:            "BlackHole2ch_UID",
			Name:           "BlackHole 2ch",
			Manufacturer:   "Existential Audio Inc.",
			InputChannels:  2,
			OutputChannels: 2,
		},
		{
			UID:  "BuiltInHeadphoneOutputDevice",
			Name: "External Headphones",
		},
	}

	resolved, err := resolveMacOSAudioDevice(devices, MacOSAudioDeviceSelectorModel{
		Name: types.StringValue("BlackHole 2ch"),
	})
	if err != nil {
		t.Fatalf("resolve name: %s", err)
	}
	if resolved.UID != "BlackHole2ch_UID" {
		t.Fatalf("uid got %q", resolved.UID)
	}

	resolved, err = resolveMacOSAudioDevice(devices, MacOSAudioDeviceSelectorModel{
		BuiltinOutput: types.StringValue("headphones"),
	})
	if err != nil {
		t.Fatalf("resolve builtin output: %s", err)
	}
	if resolved.UID != "BuiltInHeadphoneOutputDevice" {
		t.Fatalf("uid got %q", resolved.UID)
	}

	info, err := resolveMacOSAudioDeviceInfo(devices, MacOSAudioDeviceSelectorModel{
		UID: types.StringValue("BlackHole2ch_UID"),
	})
	if err != nil {
		t.Fatalf("resolve uid: %s", err)
	}
	if info.Manufacturer != "Existential Audio Inc." {
		t.Fatalf("manufacturer got %q", info.Manufacturer)
	}
	if info.OutputChannels != 2 {
		t.Fatalf("output channels got %d", info.OutputChannels)
	}
}

func TestResolveMacOSAudioDeviceSelectorsRejectsAmbiguousSelector(t *testing.T) {
	t.Parallel()

	_, err := resolveMacOSAudioDevice(nil, MacOSAudioDeviceSelectorModel{
		UID:  types.StringValue("BlackHole2ch_UID"),
		Name: types.StringValue("BlackHole 2ch"),
	})
	if err == nil {
		t.Fatal("expected selector error")
	}
}

func TestMacOSAudioMultiOutputGeneratedUID(t *testing.T) {
	t.Parallel()

	uid := macOSAudioMultiOutputGeneratedUID("Multi-Output Device")
	want := "terraform-provider-host.multi-output.multi-output-device"
	if uid != want {
		t.Fatalf("uid got %q, want %q", uid, want)
	}
}
