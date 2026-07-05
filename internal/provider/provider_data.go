package provider

type HostProviderData struct {
	HomeDir               string
	RuntimeDir            string
	TargetUser            string
	PackageManager        PackageManager
	BrewManager           BrewPackageManager
	ScheduleManager       ScheduleManager
	GitPath               string
	SSHKeyManager         SSHKeyManager
	MacOSDefaultsManager  MacOSDefaultsManager
	MacOSDockManager      MacOSDockManager
	MacOSLoginItemManager MacOSLoginItemManager
	MacOSAudioManager     MacOSAudioManager
}
