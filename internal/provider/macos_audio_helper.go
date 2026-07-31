package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	macOSAudioDeviceCacheTTL = 500 * time.Millisecond
)

type MacOSAudioDevice struct {
	UID            string `json:"uid"`
	Name           string `json:"name"`
	Manufacturer   string `json:"manufacturer"`
	InputChannels  int64  `json:"input_channels"`
	OutputChannels int64  `json:"output_channels"`
}

type MacOSAudioMultiOutputDeviceSpec struct {
	UID             string `json:"uid"`
	DriftCorrection bool   `json:"drift_correction"`
}

type MacOSAudioMultiOutputSpec struct {
	UID              string                            `json:"uid"`
	Name             string                            `json:"name"`
	PrimaryDeviceUID string                            `json:"primary_device_uid"`
	Devices          []MacOSAudioMultiOutputDeviceSpec `json:"devices"`
	SampleRateHz     int64                             `json:"sample_rate_hz"`
	DefaultOutput    bool                              `json:"default_output"`
	SystemOutput     bool                              `json:"system_output"`
}

type MacOSAudioManager interface {
	ListDevices(ctx context.Context) ([]MacOSAudioDevice, error)
	ReadMultiOutput(ctx context.Context, uid string) (MacOSAudioMultiOutputSpec, bool, error)
	WriteMultiOutput(ctx context.Context, spec MacOSAudioMultiOutputSpec) (MacOSAudioMultiOutputSpec, error)
	DeleteMultiOutput(ctx context.Context, uid string) error
}

type CLIMacOSAudioManager struct {
	swiftPath  string
	runtimeDir string
	run        macOSAudioHelperRunner

	helperMu   sync.Mutex
	helperPath string

	deviceCacheMu    sync.RWMutex
	cachedDevices    []MacOSAudioDevice
	deviceCacheUntil time.Time
	deviceListLoad   singleflight.Group
}

type macOSAudioHelperRunner func(ctx context.Context, swiftPath string, command string, payload any, output any) error

func NewCLIMacOSAudioManager(swiftPath string, runtimeDirOverride ...string) MacOSAudioManager {
	runtimeDir := ""
	if len(runtimeDirOverride) > 0 {
		runtimeDir = runtimeDirOverride[0]
	}
	return &CLIMacOSAudioManager{
		swiftPath:  swiftPath,
		runtimeDir: runtimeDir,
		run:        runMacOSAudioHelper,
	}
}

func (m *CLIMacOSAudioManager) ListDevices(ctx context.Context) ([]MacOSAudioDevice, error) {
	if devices, ok := m.readCachedDevices(); ok {
		return devices, nil
	}

	value, err, _ := m.deviceListLoad.Do("devices", func() (any, error) {
		if devices, ok := m.readCachedDevices(); ok {
			return devices, nil
		}
		var devices []MacOSAudioDevice
		if err := m.runHelper(ctx, "list-devices", nil, &devices); err != nil {
			return nil, err
		}
		m.cacheDevices(devices)
		return devices, nil
	})
	if err != nil {
		return nil, err
	}
	devices, ok := value.([]MacOSAudioDevice)
	if !ok {
		return nil, fmt.Errorf("unexpected macOS audio device result %T", value)
	}
	return cloneMacOSAudioDevices(devices), nil
}

func (m *CLIMacOSAudioManager) ReadMultiOutput(ctx context.Context, uid string) (MacOSAudioMultiOutputSpec, bool, error) {
	var result struct {
		Exists bool                      `json:"exists"`
		Spec   MacOSAudioMultiOutputSpec `json:"spec"`
	}
	if err := m.runHelper(ctx, "read-multi-output", map[string]string{"uid": uid}, &result); err != nil {
		return MacOSAudioMultiOutputSpec{}, false, err
	}
	return result.Spec, result.Exists, nil
}

func (m *CLIMacOSAudioManager) WriteMultiOutput(ctx context.Context, spec MacOSAudioMultiOutputSpec) (MacOSAudioMultiOutputSpec, error) {
	var result MacOSAudioMultiOutputSpec
	if err := m.runHelper(ctx, "write-multi-output", spec, &result); err != nil {
		return MacOSAudioMultiOutputSpec{}, err
	}
	m.invalidateDeviceCache()
	return result, nil
}

func (m *CLIMacOSAudioManager) DeleteMultiOutput(ctx context.Context, uid string) error {
	err := m.runHelper(ctx, "delete-multi-output", map[string]string{"uid": uid}, nil)
	m.invalidateDeviceCache()
	return err
}

func (m *CLIMacOSAudioManager) runHelper(ctx context.Context, command string, payload any, output any) error {
	helperPath, err := m.helperExecutable(ctx)
	if err != nil {
		return err
	}
	return m.run(ctx, helperPath, command, payload, output)
}

func runMacOSAudioHelper(ctx context.Context, helperPath string, command string, payload any, output any) error {
	args := []string{command}
	if payload != nil {
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		args = append(args, base64.StdEncoding.EncodeToString(payloadBytes))
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCommandWithExecutableBusyRetry(ctx, func() *exec.Cmd {
		stdout.Reset()
		stderr.Reset()

		cmd := exec.CommandContext(ctx, helperPath, args...)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		return cmd
	})
	if err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%w: %s", err, stderr.String())
		}
		return err
	}
	if output == nil {
		return nil
	}
	if err := json.Unmarshal(stdout.Bytes(), output); err != nil {
		return fmt.Errorf("decode macOS audio helper output: %w: %s", err, stdout.String())
	}
	return nil
}

func (m *CLIMacOSAudioManager) helperExecutable(ctx context.Context) (string, error) {
	m.helperMu.Lock()
	defer m.helperMu.Unlock()

	if executableFileExists(m.helperPath) {
		return m.helperPath, nil
	}
	if m.swiftPath == "" {
		return "", fmt.Errorf("swift compiler path is empty")
	}

	runtimeDir := m.runtimeDir
	if runtimeDir == "" {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("resolve macOS audio helper cache directory: %w", err)
		}
		runtimeDir = filepath.Join(cacheDir, providerRuntimeDirName)
	}
	helperDir, err := providerRuntimeSubdir(runtimeDir, "mac_audio")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(helperDir, 0o700); err != nil {
		return "", fmt.Errorf("create macOS audio helper directory: %w", err)
	}

	digest := sha256.Sum256([]byte(runtime.GOOS + "\x00" + runtime.GOARCH + "\x00" + macOSAudioHelperSwift))
	version := hex.EncodeToString(digest[:])
	helperPath := filepath.Join(helperDir, "helper-"+version)
	lock, err := lockHostFileContext(ctx, helperPath)
	if err != nil {
		return "", err
	}
	defer lock.close()

	if executableFileExists(helperPath) {
		m.helperPath = helperPath
		return helperPath, nil
	}
	if err := m.compileHelper(ctx, helperDir, helperPath, version); err != nil {
		return "", err
	}
	m.helperPath = helperPath
	return helperPath, nil
}

func (m *CLIMacOSAudioManager) compileHelper(ctx context.Context, helperDir string, helperPath string, version string) (returnErr error) {
	sourcePath := filepath.Join(helperDir, "helper-"+version+".swift")
	if err := writeHostFileAtomically(sourcePath, []byte(macOSAudioHelperSwift), 0o600); err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(sourcePath); err != nil && !os.IsNotExist(err) && returnErr == nil {
			returnErr = fmt.Errorf("remove macOS audio helper source: %w", err)
		}
	}()

	tempFile, err := os.CreateTemp(helperDir, ".mac-audio-helper-*")
	if err != nil {
		return fmt.Errorf("create macOS audio helper output: %w", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close macOS audio helper output: %w", err)
	}
	if err := os.Remove(tempPath); err != nil {
		return fmt.Errorf("prepare macOS audio helper output: %w", err)
	}
	defer func() {
		if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) && returnErr == nil {
			returnErr = fmt.Errorf("remove temporary macOS audio helper %q: %w", tempPath, err)
		}
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = runCommandWithExecutableBusyRetry(ctx, func() *exec.Cmd {
		stdout.Reset()
		stderr.Reset()
		cmd := exec.CommandContext(ctx, m.swiftPath, "-O", "-o", tempPath, sourcePath)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		return cmd
	})
	if err != nil {
		return fmt.Errorf(
			"compile macOS audio helper with %s: %w\n%s",
			m.swiftPath,
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	if err := os.Chmod(tempPath, 0o700); err != nil {
		return fmt.Errorf("chmod macOS audio helper: %w", err)
	}
	binary, err := os.OpenFile(tempPath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open compiled macOS audio helper: %w", err)
	}
	if err := binary.Sync(); err != nil {
		_ = binary.Close()
		return fmt.Errorf("sync compiled macOS audio helper: %w", err)
	}
	if err := binary.Close(); err != nil {
		return fmt.Errorf("close compiled macOS audio helper: %w", err)
	}
	if err := os.Rename(tempPath, helperPath); err != nil {
		return fmt.Errorf("install compiled macOS audio helper: %w", err)
	}
	return nil
}

func executableFileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Lstat(path)
	return err == nil &&
		info.Size() > 0 &&
		info.Mode().IsRegular() &&
		info.Mode()&os.ModeSymlink == 0 &&
		info.Mode().Perm()&0o111 != 0 &&
		info.Mode().Perm()&0o022 == 0
}

func (m *CLIMacOSAudioManager) readCachedDevices() ([]MacOSAudioDevice, bool) {
	m.deviceCacheMu.RLock()
	defer m.deviceCacheMu.RUnlock()
	if m.cachedDevices == nil || !time.Now().Before(m.deviceCacheUntil) {
		return nil, false
	}
	return cloneMacOSAudioDevices(m.cachedDevices), true
}

func (m *CLIMacOSAudioManager) cacheDevices(devices []MacOSAudioDevice) {
	m.deviceCacheMu.Lock()
	m.cachedDevices = cloneMacOSAudioDevices(devices)
	m.deviceCacheUntil = time.Now().Add(macOSAudioDeviceCacheTTL)
	m.deviceCacheMu.Unlock()
}

func (m *CLIMacOSAudioManager) invalidateDeviceCache() {
	m.deviceCacheMu.Lock()
	m.cachedDevices = nil
	m.deviceCacheUntil = time.Time{}
	m.deviceCacheMu.Unlock()
}

func cloneMacOSAudioDevices(devices []MacOSAudioDevice) []MacOSAudioDevice {
	return append([]MacOSAudioDevice(nil), devices...)
}

const macOSAudioHelperSwift = `
import Foundation
import CoreAudio

struct AudioDeviceInfo: Codable {
    let uid: String
    let name: String
    let manufacturer: String
    let input_channels: Int64
    let output_channels: Int64
}

struct MultiOutputSubdevice: Codable {
    let uid: String
    let drift_correction: Bool
}

struct MultiOutputSpec: Codable {
    let uid: String
    let name: String
    let primary_device_uid: String
    let devices: [MultiOutputSubdevice]
    let sample_rate_hz: Int64
    let default_output: Bool
    let system_output: Bool
}

struct ReadRequest: Codable {
    let uid: String
}

struct ReadResult: Codable {
    let exists: Bool
    let spec: MultiOutputSpec
}

func fail(_ message: String) -> Never {
    FileHandle.standardError.write((message + "\n").data(using: .utf8)!)
    exit(1)
}

func decodePayload<T: Decodable>(_ type: T.Type) -> T {
    guard CommandLine.arguments.count >= 3 else {
        fail("missing helper payload")
    }
    guard let data = Data(base64Encoded: CommandLine.arguments[2]) else {
        fail("invalid helper payload encoding")
    }
    do {
        return try JSONDecoder().decode(T.self, from: data)
    } catch {
        fail("decode helper payload: \(error)")
    }
}

func writeJSON<T: Encodable>(_ value: T) {
    do {
        let data = try JSONEncoder().encode(value)
        FileHandle.standardOutput.write(data)
    } catch {
        fail("encode helper output: \(error)")
    }
}

func check(_ status: OSStatus, _ operation: String) {
    if status != noErr {
        fail("\(operation) failed: OSStatus \(status)")
    }
}

func getString(_ objectID: AudioObjectID, _ selector: AudioObjectPropertySelector) -> String {
    var address = AudioObjectPropertyAddress(mSelector: selector, mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
    var size = UInt32(MemoryLayout<CFString?>.size)
    var value: CFString? = nil
    let status = AudioObjectGetPropertyData(objectID, &address, 0, nil, &size, &value)
    if status != noErr {
        return ""
    }
    return (value as String?) ?? ""
}

func getFloat64(_ objectID: AudioObjectID, _ selector: AudioObjectPropertySelector) -> Float64 {
    var address = AudioObjectPropertyAddress(mSelector: selector, mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
    var size = UInt32(MemoryLayout<Float64>.size)
    var value = Float64(0)
    let status = AudioObjectGetPropertyData(objectID, &address, 0, nil, &size, &value)
    if status != noErr {
        return 0
    }
    return value
}

func getDeviceID(_ selector: AudioObjectPropertySelector) -> AudioDeviceID {
    var address = AudioObjectPropertyAddress(mSelector: selector, mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
    var size = UInt32(MemoryLayout<AudioDeviceID>.size)
    var value = AudioDeviceID(kAudioObjectUnknown)
    let status = AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size, &value)
    if status != noErr {
        return AudioDeviceID(kAudioObjectUnknown)
    }
    return value
}

func setDeviceID(_ selector: AudioObjectPropertySelector, _ value: AudioDeviceID) {
    var address = AudioObjectPropertyAddress(mSelector: selector, mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
    var size = UInt32(MemoryLayout<AudioDeviceID>.size)
    var mutableValue = value
    check(AudioObjectSetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, size, &mutableValue), "set default audio device")
}

func setFloat64(_ objectID: AudioObjectID, _ selector: AudioObjectPropertySelector, _ value: Float64) {
    var address = AudioObjectPropertyAddress(mSelector: selector, mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
    var size = UInt32(MemoryLayout<Float64>.size)
    var mutableValue = value
    check(AudioObjectSetPropertyData(objectID, &address, 0, nil, size, &mutableValue), "set audio device value")
}

func channelCount(_ objectID: AudioObjectID, _ scope: AudioObjectPropertyScope) -> Int64 {
    var address = AudioObjectPropertyAddress(mSelector: kAudioDevicePropertyStreamConfiguration, mScope: scope, mElement: kAudioObjectPropertyElementMain)
    var size: UInt32 = 0
    let sizeStatus = AudioObjectGetPropertyDataSize(objectID, &address, 0, nil, &size)
    if sizeStatus != noErr || size == 0 {
        return 0
    }
    let bufferList = UnsafeMutablePointer<AudioBufferList>.allocate(capacity: Int(size))
    defer { bufferList.deallocate() }
    let dataStatus = AudioObjectGetPropertyData(objectID, &address, 0, nil, &size, bufferList)
    if dataStatus != noErr {
        return 0
    }
    let buffers = UnsafeMutableAudioBufferListPointer(bufferList)
    return buffers.reduce(Int64(0)) { $0 + Int64($1.mNumberChannels) }
}

func deviceIDs() -> [AudioDeviceID] {
    var address = AudioObjectPropertyAddress(mSelector: kAudioHardwarePropertyDevices, mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
    var size: UInt32 = 0
    check(AudioObjectGetPropertyDataSize(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size), "read audio device list size")
    let count = Int(size) / MemoryLayout<AudioDeviceID>.size
    var devices = [AudioDeviceID](repeating: 0, count: count)
    check(AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size, &devices), "read audio device list")
    return devices
}

func listDevices() -> [AudioDeviceInfo] {
    return deviceIDs().map {
        AudioDeviceInfo(
            uid: getString($0, kAudioDevicePropertyDeviceUID),
            name: getString($0, kAudioObjectPropertyName),
            manufacturer: getString($0, kAudioObjectPropertyManufacturer),
            input_channels: channelCount($0, kAudioObjectPropertyScopeInput),
            output_channels: channelCount($0, kAudioObjectPropertyScopeOutput)
        )
    }
}

func findDeviceID(uid: String) -> AudioDeviceID? {
    for id in deviceIDs() {
        if getString(id, kAudioDevicePropertyDeviceUID) == uid {
            return id
        }
    }
    return nil
}

func getComposition(_ objectID: AudioObjectID) -> NSDictionary? {
    var address = AudioObjectPropertyAddress(mSelector: kAudioAggregateDevicePropertyComposition, mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
    var size = UInt32(MemoryLayout<CFDictionary?>.size)
    var value: CFDictionary? = nil
    let status = AudioObjectGetPropertyData(objectID, &address, 0, nil, &size, &value)
    if status != noErr {
        return nil
    }
    return value as NSDictionary?
}

func readMultiOutput(uid: String) -> (Bool, MultiOutputSpec) {
    guard let objectID = findDeviceID(uid: uid), let composition = getComposition(objectID) else {
        return (false, MultiOutputSpec(uid: uid, name: "", primary_device_uid: "", devices: [], sample_rate_hz: 0, default_output: false, system_output: false))
    }
    let isStacked = (composition["stacked"] as? NSNumber)?.boolValue ?? false
    if !isStacked {
        return (false, MultiOutputSpec(uid: uid, name: "", primary_device_uid: "", devices: [], sample_rate_hz: 0, default_output: false, system_output: false))
    }

    let subdeviceDictionaries = composition["subdevices"] as? [NSDictionary] ?? []
    let subdevices = subdeviceDictionaries.compactMap { item -> MultiOutputSubdevice? in
        guard let subUID = item["uid"] as? String else {
            return nil
        }
        let drift = (item["drift"] as? NSNumber)?.boolValue ?? false
        return MultiOutputSubdevice(uid: subUID, drift_correction: drift)
    }
    let defaultOutputID = getDeviceID(kAudioHardwarePropertyDefaultOutputDevice)
    let systemOutputID = getDeviceID(kAudioHardwarePropertyDefaultSystemOutputDevice)
    let spec = MultiOutputSpec(
        uid: uid,
        name: (composition["name"] as? String) ?? getString(objectID, kAudioObjectPropertyName),
        primary_device_uid: (composition["master"] as? String) ?? "",
        devices: subdevices,
        sample_rate_hz: Int64(getFloat64(objectID, kAudioDevicePropertyNominalSampleRate)),
        default_output: defaultOutputID == objectID,
        system_output: systemOutputID == objectID
    )
    return (true, spec)
}

func destroyMultiOutput(uid: String) {
    if let objectID = findDeviceID(uid: uid), getComposition(objectID) != nil {
        check(AudioHardwareDestroyAggregateDevice(objectID), "destroy aggregate audio device")
    }
}

func sameMultiOutputComposition(_ actual: MultiOutputSpec, _ desired: MultiOutputSpec) -> Bool {
    if actual.name != desired.name || actual.primary_device_uid != desired.primary_device_uid {
        return false
    }
    if actual.devices.count != desired.devices.count {
        return false
    }
    for index in actual.devices.indices {
        if actual.devices[index].uid != desired.devices[index].uid {
            return false
        }
        if actual.devices[index].drift_correction != desired.devices[index].drift_correction {
            return false
        }
    }
    return true
}

func finishMultiOutput(_ objectID: AudioObjectID, _ spec: MultiOutputSpec) -> MultiOutputSpec {
    if spec.sample_rate_hz > 0 {
        setFloat64(objectID, kAudioDevicePropertyNominalSampleRate, Float64(spec.sample_rate_hz))
    }
    if spec.default_output {
        setDeviceID(kAudioHardwarePropertyDefaultOutputDevice, objectID)
    }
    if spec.system_output {
        setDeviceID(kAudioHardwarePropertyDefaultSystemOutputDevice, objectID)
    }
    let (exists, next) = readMultiOutput(uid: spec.uid)
    if !exists {
        fail("multi-output device did not appear in CoreAudio device list")
    }
    return next
}

func writeMultiOutput(_ spec: MultiOutputSpec) -> MultiOutputSpec {
    if let objectID = findDeviceID(uid: spec.uid) {
        let (exists, actual) = readMultiOutput(uid: spec.uid)
        if exists && sameMultiOutputComposition(actual, spec) {
            return finishMultiOutput(objectID, spec)
        }
    }

    destroyMultiOutput(uid: spec.uid)
    let subdevices = spec.devices.map {
        [
            "uid": $0.uid,
            "drift": NSNumber(value: $0.drift_correction),
            "drift quality": NSNumber(value: 127)
        ] as [String : Any]
    }
    let description = [
        "uid": spec.uid,
        "name": spec.name,
        "stacked": NSNumber(value: true),
        "master": spec.primary_device_uid,
        "subdevices": subdevices
    ] as [String : Any]

    var objectID = AudioObjectID(kAudioObjectUnknown)
    check(AudioHardwareCreateAggregateDevice(description as CFDictionary, &objectID), "create multi-output audio device")
    return finishMultiOutput(objectID, spec)
}

guard CommandLine.arguments.count >= 2 else {
    fail("missing helper command")
}

switch CommandLine.arguments[1] {
case "list-devices":
    writeJSON(listDevices())
case "read-multi-output":
    let request = decodePayload(ReadRequest.self)
    let (exists, spec) = readMultiOutput(uid: request.uid)
    writeJSON(ReadResult(exists: exists, spec: spec))
case "write-multi-output":
    let spec = decodePayload(MultiOutputSpec.self)
    writeJSON(writeMultiOutput(spec))
case "delete-multi-output":
    let request = decodePayload(ReadRequest.self)
    destroyMultiOutput(uid: request.uid)
case let command:
    fail("unknown helper command \(command)")
}
`
