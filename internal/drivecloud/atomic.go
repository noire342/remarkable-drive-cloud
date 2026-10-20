// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

func atomicWrite(name string, mode os.FileMode, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".pending-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		err = write(f)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, name); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		d, e := os.Open(filepath.Dir(name))
		if e != nil {
			return e
		}
		defer d.Close()
		return d.Sync()
	}
	return nil
}
func saveJSON(name string, v any) error {
	return atomicWrite(name, 0600, func(w io.Writer) error { return json.NewEncoder(w).Encode(v) })
}
func loadJSON(name string, v any) error {
	b, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
