package provider

type HostProviderData struct {
	HomeDir               string
	RuntimeDir            string
	TargetUser            string
	IdentityManager       IdentityManager
	PackageManager        PackageManager
	PacmanManager         PackageManager
	AURManager            AURPackageManager
	AURHelperManager      AURHelperManager
	BrewManager           BrewPackageManager
	HostnameManager       HostnameManager
	TimezoneManager       TimezoneManager
	LocaleManager         LocaleManager
	KeymapManager         KeymapManager
	SystemdManager        SystemdServiceManager
	SystemdUnitManager    SystemdUnitManager
	SysctlManager         SysctlManager
	FstabManager          FstabManager
	ScheduleManager       ScheduleManager
	GitPath               string
	SSHKeyManager         SSHKeyManager
	MacOSDefaultsManager  MacOSDefaultsManager
	MacOSDockManager      MacOSDockManager
	MacOSLoginItemManager MacOSLoginItemManager
	MacOSAudioManager     MacOSAudioManager
}
