// SPDX-License-Identifier: AGPL-3.0-only
package deviceops

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// This evidence is deliberately private and bound to one exact firmware.
// Compilation and protocol simulations cannot set physical-device results.
type Validation struct {
	Firmware         string    `json:"firmware"`
	XochitlHash      string    `json:"xochitl_sha256"`
	DeviceHash       string    `json:"device_identity_sha256"`
	ProtocolPhysical bool      `json:"native_protocol_physical"`
	RestorePhysical  bool      `json:"native_restore_physical"`
	CloudInventory   bool      `json:"official_cloud_inventory_verified"`
	BackupStable     bool      `json:"backup_source_stable"`
	BackupHash       string    `json:"backup_manifest_sha256"`
	DriveVerified    bool      `json:"drive_snapshot_verified"`
	UIPhysical       bool      `json:"native_settings_physical"`
	Checked          time.Time `json:"checked"`
}

func ReadValidation(file, deviceHash, backupHash string, now time.Time) (Validation, error) {
	var v Validation
	b, err := os.ReadFile(file)
	if err != nil {
		return v, fmt.Errorf("migration blocked: physical validation evidence missing")
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("migration blocked: invalid evidence")
	}
	if v.Firmware != Firmware || v.XochitlHash != XochitlSHA256 || v.DeviceHash != deviceHash || v.BackupHash != backupHash || len(deviceHash) != 64 || len(backupHash) != 64 || !v.ProtocolPhysical || !v.RestorePhysical || !v.CloudInventory || !v.BackupStable || !v.DriveVerified || !v.UIPhysical || v.Checked.After(now) || now.Sub(v.Checked) > 24*time.Hour {
		return v, fmt.Errorf("migration blocked: device, backup, protocol, restore, Drive or UI validation incomplete")
	}
	return v, nil
}
