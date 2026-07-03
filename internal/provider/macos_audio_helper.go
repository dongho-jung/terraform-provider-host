package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
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
	swiftPath string
	run       macOSAudioHelperRunner
}

type macOSAudioHelperRunner func(ctx context.Context, swiftPath string, command string, payload any, output any) error

func NewCLIMacOSAudioManager(swiftPath string) MacOSAudioManager {
	return &CLIMacOSAudioManager{
		swiftPath: swiftPath,
		run:       runMacOSAudioHelper,
	}
}

func (m *CLIMacOSAudioManager) ListDevices(ctx context.Context) ([]MacOSAudioDevice, error) {
	var devices []MacOSAudioDevice
	if err := m.run(ctx, m.swiftPath, "list-devices", nil, &devices); err != nil {
		return nil, err
	}
	return devices, nil
}

func (m *CLIMacOSAudioManager) ReadMultiOutput(ctx context.Context, uid string) (MacOSAudioMultiOutputSpec, bool, error) {
	var result struct {
		Exists bool                      `json:"exists"`
		Spec   MacOSAudioMultiOutputSpec `json:"spec"`
	}
	if err := m.run(ctx, m.swiftPath, "read-multi-output", map[string]string{"uid": uid}, &result); err != nil {
		return MacOSAudioMultiOutputSpec{}, false, err
	}
	return result.Spec, result.Exists, nil
}

func (m *CLIMacOSAudioManager) WriteMultiOutput(ctx context.Context, spec MacOSAudioMultiOutputSpec) (MacOSAudioMultiOutputSpec, error) {
	var result MacOSAudioMultiOutputSpec
	if err := m.run(ctx, m.swiftPath, "write-multi-output", spec, &result); err != nil {
		return MacOSAudioMultiOutputSpec{}, err
	}
	return result, nil
}

func (m *CLIMacOSAudioManager) DeleteMultiOutput(ctx context.Context, uid string) error {
	return m.run(ctx, m.swiftPath, "delete-multi-output", map[string]string{"uid": uid}, nil)
}

func runMacOSAudioHelper(ctx context.Context, swiftPath string, command string, payload any, output any) error {
	args := []string{"-suppress-warnings", "-", command}
	if payload != nil {
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		args = append(args, base64.StdEncoding.EncodeToString(payloadBytes))
	}

	cmd := exec.CommandContext(ctx, swiftPath, args...)
	cmd.Stdin = bytes.NewBufferString(macOSAudioHelperSwift)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
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
