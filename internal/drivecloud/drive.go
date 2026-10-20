// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const fileFields = "id,name,mimeType,parents,trashed,md5Checksum,sha256Checksum,size,version,appProperties"

type Drive struct {
	HTTP       *http.Client
	Base       string
	UploadBase string
	Sessions   string
	retryWait  func(context.Context, time.Duration) error
}
type uploadSession struct {
	URL  string `json:"url"`
	ID   string `json:"id"`
	MD5  string `json:"md5"`
	Size int64  `json:"size"`
}
type HTTPError struct {
	Status     int
	Reason     string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("Google Drive HTTP %d (%s)", e.Status, e.Reason)
}
func NewDrive(httpClient *http.Client, sessions string) *Drive {
	return &Drive{HTTP: httpClient, Base: "https://www.googleapis.com/drive/v3", UploadBase: "https://www.googleapis.com/upload/drive/v3", Sessions: sessions}
}
func (d *Drive) requestOnce(ctx context.Context, method, u string, body io.Reader, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "remarkable-drive-cloud/0.1.0-dev")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if value, ok := headers["Content-Length"]; ok {
		req.ContentLength, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, err
		}
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, ErrNotLinked) {
			return nil, ErrNotLinked
		}
		return nil, fmt.Errorf("Google Drive connection interrupted")
	}
	if resp.StatusCode == 404 {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 400 {
		var body struct {
			Error struct {
				Errors []struct {
					Reason string `json:"reason"`
				} `json:"errors"`
			} `json:"error"`
		}
		json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode == 401 {
			return nil, ErrNotLinked
		}
		reason := "requestFailed"
		if len(body.Error.Errors) > 0 && regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`).MatchString(body.Error.Errors[0].Reason) {
			reason = body.Error.Errors[0].Reason
		}
		return nil, &HTTPError{Status: resp.StatusCode, Reason: reason, RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
	return resp, nil
}
func (d *Drive) json(ctx context.Context, method, u string, body any, result any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	resp, err := d.request(ctx, method, u, reader, map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if result == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(result)
}
func (d *Drive) Get(ctx context.Context, id string) (f File, err error) {
	err = d.json(ctx, "GET", d.Base+"/files/"+url.PathEscape(id)+"?fields="+url.QueryEscape(fileFields), nil, &f)
	return
}
func (d *Drive) List(ctx context.Context, q string) (files []File, err error) {
	token := ""
	for {
		var result struct {
			Files []File `json:"files"`
			Next  string `json:"nextPageToken"`
		}
		params := url.Values{"q": {q}, "fields": {"nextPageToken,files(" + fileFields + ")"}, "pageSize": {"1000"}}
		if token != "" {
			params.Set("pageToken", token)
		}
		if err = d.json(ctx, "GET", d.Base+"/files?"+params.Encode(), nil, &result); err != nil {
			return
		}
		files = append(files, result.Files...)
		token = result.Next
		if token == "" {
			return
		}
	}
}
func (d *Drive) Download(ctx context.Context, id string) (io.ReadCloser, error) {
	resp, err := d.request(ctx, "GET", d.Base+"/files/"+url.PathEscape(id)+"?alt=media", nil, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}
func (d *Drive) NewID(ctx context.Context) (string, error) {
	var v struct {
		IDs []string `json:"ids"`
	}
	if err := d.json(ctx, "GET", d.Base+"/files/generateIds?count=1&space=drive&type=files", nil, &v); err != nil {
		return "", err
	}
	if len(v.IDs) != 1 {
		return "", errors.New("Google Drive returned no file ID")
	}
	return v.IDs[0], nil
}
func (d *Drive) Patch(ctx context.Context, id string, fields map[string]any, add, remove string) (f File, err error) {
	q := url.Values{"fields": {fileFields}}
	if add != "" {
		q.Set("addParents", add)
	}
	if remove != "" {
		q.Set("removeParents", remove)
	}
	err = d.json(ctx, "PATCH", d.Base+"/files/"+url.PathEscape(id)+"?"+q.Encode(), fields, &f)
	return
}
func (d *Drive) StartToken(ctx context.Context) (string, error) {
	var v struct {
		Token string `json:"startPageToken"`
	}
	err := d.json(ctx, "GET", d.Base+"/changes/startPageToken", nil, &v)
	return v.Token, err
}
func (d *Drive) Changes(ctx context.Context, token string) (v Changes, err error) {
	q := url.Values{"pageToken": {token}, "pageSize": {"1000"}, "includeRemoved": {"true"}, "fields": {"nextPageToken,newStartPageToken,changes(fileId,removed,file(" + fileFields + "))"}}
	err = d.json(ctx, "GET", d.Base+"/changes?"+q.Encode(), nil, &v)
	return
}
func (d *Drive) Account(ctx context.Context) (string, Quota, error) {
	var v struct {
		User struct {
			Email string `json:"emailAddress"`
		} `json:"user"`
		Quota Quota `json:"storageQuota"`
	}
	err := d.json(ctx, "GET", d.Base+"/about?fields=user(emailAddress),storageQuota", nil, &v)
	return v.User.Email, v.Quota, err
}

func fileMD5(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := md5.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}
func (d *Drive) validSession(raw string) bool {
	u, e := url.Parse(raw)
	base, _ := url.Parse(d.UploadBase)
	return e == nil && u.User == nil && u.Fragment == "" && u.Scheme == base.Scheme && u.Host == base.Host && strings.HasPrefix(u.Path, "/upload/")
}
func (d *Drive) Put(ctx context.Context, meta File, path string) (File, error) {
	if meta.ID == "" {
		return File{}, errors.New("durable Drive ID required before create")
	}
	existing, getErr := d.Get(ctx, meta.ID)
	if getErr != nil && !errors.Is(getErr, ErrNotFound) {
		return File{}, getErr
	}
	if meta.MIME == FolderMIME {
		if getErr == nil {
			if existing.Trashed || existing.MIME != FolderMIME {
				return File{}, ErrSuspended
			}
			return existing, nil
		}
		var f File
		err := d.json(ctx, "POST", d.Base+"/files?fields="+url.QueryEscape(fileFields), meta, &f)
		return f, err
	}
	md5sum, size, err := fileMD5(path)
	if err != nil {
		return File{}, err
	}
	if getErr == nil && existing.MD5 == md5sum && !existing.Trashed {
		return existing, nil
	}
	immutable := meta.Properties["role"] == "object" || meta.Properties["role"] == "snapshot"
	if getErr == nil && immutable {
		return File{}, fmt.Errorf("immutable remote object changed: %w", ErrSuspended)
	}
	sessionPath := filepath.Join(d.Sessions, meta.ID+".json")
	var session uploadSession
	if loadJSON(sessionPath, &session) != nil || session.ID != meta.ID || session.MD5 != md5sum || session.Size != size || !d.validSession(session.URL) {
		session = uploadSession{ID: meta.ID, MD5: md5sum, Size: size}
	}
	for attempt := 0; attempt < 2; attempt++ {
		if session.URL == "" {
			method := "POST"
			u := d.UploadBase + "/files"
			bodyMeta := meta
			if getErr == nil {
				method = "PATCH"
				u += "/" + url.PathEscape(meta.ID)
				bodyMeta.ID = ""
				bodyMeta.Parents = nil
			}
			b, _ := json.Marshal(bodyMeta)
			resp, e := d.request(ctx, method, u+"?uploadType=resumable&fields="+url.QueryEscape(fileFields), bytes.NewReader(b), map[string]string{"Content-Type": "application/json", "X-Upload-Content-Type": meta.MIME, "X-Upload-Content-Length": strconv.FormatInt(size, 10)})
			if e != nil {
				return File{}, e
			}
			session.URL = resp.Header.Get("Location")
			resp.Body.Close()
			if !d.validSession(session.URL) {
				return File{}, errors.New("untrusted Google upload session")
			}
			if e = saveJSON(sessionPath, session); e != nil {
				return File{}, e
			}
		}
		offset := int64(0)
		statusHeaders := map[string]string{"Content-Range": fmt.Sprintf("bytes */%d", size), "Content-Length": "0"}
		if size == 0 {
			delete(statusHeaders, "Content-Range")
			statusHeaders["Content-Type"] = meta.MIME
		}
		resp, e := d.request(ctx, "PUT", session.URL, bytes.NewReader(nil), statusHeaders)
		if errors.Is(e, ErrNotFound) {
			session.URL = ""
			continue
		}
		if e != nil {
			return File{}, e
		}
		if resp.StatusCode == 200 || resp.StatusCode == 201 {
			var f File
			e = json.NewDecoder(resp.Body).Decode(&f)
			resp.Body.Close()
			if e != nil {
				return File{}, e
			}
			if f.MD5 != md5sum {
				return File{}, errors.New("uploaded checksum mismatch")
			}
			os.Remove(sessionPath)
			return f, nil
		}
		if resp.StatusCode != 308 {
			resp.Body.Close()
			return File{}, fmt.Errorf("invalid upload status %d", resp.StatusCode)
		}
		if r := resp.Header.Get("Range"); r != "" {
			if _, e = fmt.Sscanf(r, "bytes=0-%d", &offset); e != nil {
				resp.Body.Close()
				return File{}, errors.New("invalid resumable upload range")
			}
			offset++
		}
		resp.Body.Close()
		if offset < 0 || offset > size {
			return File{}, errors.New("invalid resumable upload offset")
		}
		input, e := os.Open(path)
		if e != nil {
			return File{}, e
		}
		for offset < size {
			chunk := int64(4 << 20)
			if size-offset < chunk {
				chunk = size - offset
			}
			_, e = input.Seek(offset, io.SeekStart)
			if e != nil {
				input.Close()
				return File{}, e
			}
			resp, e = d.request(ctx, "PUT", session.URL, io.NewSectionReader(input, offset, chunk), map[string]string{"Content-Length": strconv.FormatInt(chunk, 10), "Content-Range": fmt.Sprintf("bytes %d-%d/%d", offset, offset+chunk-1, size), "Content-Type": meta.MIME})
			if e != nil {
				input.Close()
				return File{}, e
			}
			if resp.StatusCode == 200 || resp.StatusCode == 201 {
				var f File
				e = json.NewDecoder(resp.Body).Decode(&f)
				resp.Body.Close()
				input.Close()
				if e != nil {
					return File{}, e
				}
				if f.MD5 != md5sum {
					return File{}, errors.New("uploaded checksum mismatch")
				}
				os.Remove(sessionPath)
				return f, nil
			}
			ack := resp.Header.Get("Range")
			resp.Body.Close()
			if resp.StatusCode != 308 {
				input.Close()
				return File{}, fmt.Errorf("upload HTTP %d", resp.StatusCode)
			}
			var last int64
			if _, err := fmt.Sscanf(ack, "bytes=0-%d", &last); err != nil || last+1 <= offset || last+1 > offset+chunk {
				input.Close()
				return File{}, errors.New("invalid or non-progressing upload acknowledgement")
			}
			offset = last + 1
		}
		input.Close()
		return File{}, errors.New("upload incomplete; retry status query")
	}
	return File{}, errors.New("upload session expired; retry")
}
