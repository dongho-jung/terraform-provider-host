package provider

type HostProviderData struct {
	PackageManager       PackageManager
	BrewManager          BrewPackageManager
	ScheduleManager      ScheduleManager
	IdentityManager      IdentityManager
	GitPath              string
	MacOSDefaultsManager MacOSDefaultsManager
	MacOSDockManager     MacOSDockManager
	MacOSAudioManager    MacOSAudioManager
}
