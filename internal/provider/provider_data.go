package provider

type HostProviderData struct {
	HomeDir               string
	RuntimeDir            string
	PackageManager        PackageManager
	BrewManager           BrewPackageManager
	ScheduleManager       ScheduleManager
	IdentityManager       IdentityManager
	GitPath               string
	SSHKeyManager         SSHKeyManager
	MacOSDefaultsManager  MacOSDefaultsManager
	MacOSDockManager      MacOSDockManager
	MacOSLoginItemManager MacOSLoginItemManager
	MacOSAudioManager     MacOSAudioManager
}
