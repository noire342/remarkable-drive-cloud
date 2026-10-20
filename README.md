# reMarkable Drive Cloud

An experimental, autonomous Google Drive document cloud for **reMarkable 2,
firmware 3.27.3.0**. Based on a pinned fork of rmfakecloud; AGPL-3.0.

**Development only. No stable release or production migration is available yet.**
The native `rm-sync` client talks to a local bridge. A committed native root is
acknowledged only after its complete immutable snapshot has reached Drive.
Xochitl remains responsible for applying document changes to its live library.

The intended final configuration disconnects the official reMarkable document
cloud and replaces the native **Settings → Cloud** page with Google Drive.
The installer currently installs the bridge and page in preparation mode. It
does not yet perform a production account cutover. That cutover requires physical
protocol/restore tests, a complete old-cloud inventory and verified Drive backup.
Do not delete your existing account or copy files into an active Xochitl library.

## Implemented development components

- Persistent, serialized native object staging; verified immutable Drive snapshots;
  stable Drive IDs, resumable uploads and retry after lost acknowledgements.
- Drive `reMarkable/Libreria`, `Da importare`, `_Archivio`; native UUID mappings,
  folder/name/trash propagation, import receipts, conflict copies and separate
  imports of externally edited PDF content.
- Raw native metadata preservation, both legacy structural index hashes and
  current schema-4 byte hashes, native PDF rendering, history and copy restore.
- Local authenticated control API, desktop OAuth with PKCE, private credentials.
- Native Cloud page using XOVI; bounded installer validation with UI rollback.
  Quick Share and other extensions remain independent.

Read [current validation and remaining gates](docs/VALIDATION.md) before use.
Rendering or conversion by UniPDF is absent. OCR, handwriting search, AI, email,
official companion apps and multi-tablet synchronization are outside this version.

## Build and prepare

Requires Go 1.27.2, Python 3 and an OpenSSH client on your computer:

```sh
go test -tags cloud ./...
go vet -tags cloud ./...
python scripts/build_development.py
```

On Windows, run `./install.ps1 -Tablet IP_TABLET`; on Linux/macOS,
`sh install.sh IP_TABLET`, from the built checkout. The wrappers select the host
helper, verify its checksum and transfer the verified ARMv7 bundle over SSH.
There is intentionally no public curl-to-install command or stable download yet.
Keep the tablet unlocked while the firmware QML table is generated.

Configure your **personal Desktop OAuth client** using
[the Italian guide](docs/GOOGLE_SETUP.it.md). Authorization and installation do
not by themselves migrate the cloud. The program's broad Drive permission is
required to detect files users add directly to the inbox; application operations
are restricted to the dedicated vault IDs. Credentials and backups never belong
in this repository.

See [privacy and Google Drive access](docs/PRIVACY.md) for data flows,
credentials, retention and revocation.

## Source and licenses

Upstream base: `ddvk/rmfakecloud@3c6de006bdc465bdc0aa14b4de8fac78b5096ee6`.
The Go module path remains upstream's to preserve fork imports; this does not
imply upstream authorship of the Drive integration. See
[third-party notices](THIRD_PARTY_NOTICES.md) and [AGPL-3.0](LICENSE).
Firmware, user documents, OAuth credentials and XOVI binaries are not shipped.
