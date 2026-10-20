// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"context"
	"errors"
	"io"
	"time"
)

const FolderMIME = "application/vnd.google-apps.folder"
const FormatVersion = 1

var ErrSuspended = errors.New("cloud synchronization suspended")
var ErrNotFound = errors.New("remote file not found")
var ErrNotLinked = errors.New("Google Drive account not linked")

type File struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	MIME       string            `json:"mimeType"`
	Parents    []string          `json:"parents,omitempty"`
	Trashed    bool              `json:"trashed,omitempty"`
	MD5        string            `json:"md5Checksum,omitempty"`
	SHA256     string            `json:"sha256Checksum,omitempty"`
	Size       string            `json:"size,omitempty"`
	Version    string            `json:"version,omitempty"`
	Properties map[string]string `json:"appProperties,omitempty"`
}
type Change struct {
	FileID  string `json:"fileId"`
	Removed bool   `json:"removed"`
	File    *File  `json:"file,omitempty"`
}
type Changes struct {
	Changes []Change `json:"changes"`
	Next    string   `json:"nextPageToken"`
	Start   string   `json:"newStartPageToken"`
}
type Quota struct {
	Limit string `json:"limit"`
	Usage string `json:"usage"`
}
type Remote interface {
	Get(context.Context, string) (File, error)
	List(context.Context, string) ([]File, error)
	Download(context.Context, string) (io.ReadCloser, error)
	NewID(context.Context) (string, error)
	Put(context.Context, File, string) (File, error)
	Patch(context.Context, string, map[string]any, string, string) (File, error)
	StartToken(context.Context) (string, error)
	Changes(context.Context, string) (Changes, error)
	Account(context.Context) (string, Quota, error)
}
type Layout struct {
	Root      string `json:"root"`
	Library   string `json:"library"`
	Inbox     string `json:"inbox"`
	Archive   string `json:"archive"`
	Objects   string `json:"objects"`
	Snapshots string `json:"snapshots"`
}
type Object struct {
	ID     string `json:"drive_id"`
	SHA256 string `json:"sha256"`
	MD5    string `json:"md5"`
	Size   int64  `json:"size"`
}
type Snapshot struct {
	Names      map[string]string `json:"document_names"`
	Version    int               `json:"format_version"`
	Vault      string            `json:"vault_id"`
	Writer     string            `json:"writer_id"`
	ID         string            `json:"snapshot_id"`
	Previous   string            `json:"previous_snapshot,omitempty"`
	Generation int64             `json:"generation"`
	Root       string            `json:"native_root"`
	Created    time.Time         `json:"created"`
	Objects    map[string]Object `json:"objects"`
	Documents  map[string]string `json:"documents"`
}
type Pending struct {
	Root     string   `json:"root"`
	Base     int64    `json:"base"`
	Snapshot Snapshot `json:"snapshot"`
}
type Mapping struct {
	PendingHash   string `json:"pending_hash,omitempty"`
	PendingMD5    string `json:"pending_md5,omitempty"`
	PendingName   string `json:"pending_name,omitempty"`
	PendingParent string `json:"pending_parent,omitempty"`
	NativeID      string `json:"native_id"`
	DriveID       string `json:"drive_id"`
	Name          string `json:"name"`
	Parent        string `json:"parent"`
	Trashed       bool   `json:"trashed"`
	NativeHash    string `json:"native_hash"`
	MD5           string `json:"md5"`
	DriveVersion  string `json:"drive_version"`
	ExportHash    string `json:"export_hash"`
}
type State struct {
	Active      bool               `json:"active"`
	Initialized bool               `json:"initialized"`
	Version     int                `json:"version"`
	Vault       string             `json:"vault_id"`
	Writer      string             `json:"writer_id"`
	User        string             `json:"native_user"`
	Layout      Layout             `json:"layout"`
	Current     Snapshot           `json:"current"`
	Pending     *Pending           `json:"pending,omitempty"`
	Mappings    map[string]Mapping `json:"mappings"`
	Receipts    map[string]string  `json:"receipts"`
	Cursor      string             `json:"cursor"`
	Paused      bool               `json:"paused"`
	LastError   string             `json:"last_error,omitempty"`
	LastSync    time.Time          `json:"last_sync"`
	Account     string             `json:"account"`
	Quota       Quota              `json:"quota"`
}
type Status struct {
	Active     bool      `json:"active"`
	Linked     bool      `json:"linked"`
	Ready      bool      `json:"ready"`
	Paused     bool      `json:"paused"`
	Account    string    `json:"account"`
	Generation int64     `json:"generation"`
	Pending    int       `json:"pending"`
	LastSync   time.Time `json:"last_sync"`
	LastError  string    `json:"last_error"`
	Quota      Quota     `json:"quota"`
}
