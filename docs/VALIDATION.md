# Development validation

This file separates implementation, simulation and physical acceptance.
The first production release is gated on **all** physical checks below.

## Verified during development, 2026-10-20

- Host Go tests, including snapshot ordering, incomplete graphs, generation
  conflicts, corrupt state, offline/retry, lost upload acknowledgements, import
  receipts, external PDF conflicts and copy restore.
- A real tablet's read-only export: 18 native RMDOC documents plus one folder.
  Archive CRCs, UUIDs, stable source hashes and the complete parsed graph verified.
- The official account's read-only inventory: the same 19 items, no remote-only
  UUIDs. A separate 106-object cloud backup was downloaded and hashes verified.
  The new parser validates its complete mixed schema-3/schema-4 tree.
- Personal Google Desktop OAuth authorization completed through Edge. A first
  native snapshot of all 19 items was committed to the dedicated Drive archive;
  all 105 referenced objects were downloaded again and SHA-256/size checked.
  A reconstructed private backup was parsed again with identical native root and
  all 19 UUIDs. This is a host reconstruction, not a physical tablet restore.
- The personal OAuth application's public branding references were completed.
  After explicit owner authorization, its Google status changed from Testing
  to Production and a fresh offline grant was verified against the intended
  account. Credentials were transferred through pinned SSH with a backup and
  atomic replacement; the native UI was not restarted. A scan of all tracked
  files and the development bundle found none of this client's identifiers,
  client secret, access token or refresh token. Every user supplies their own
  OAuth client; no shared client is distributed.
- ARMv7 tablet and Windows/macOS/Linux amd64/arm64 setup binaries built.
- Settings > Cloud installed on the exact device and its authenticated readiness
  checked. The personal Drive credentials were transferred privately over pinned
  SSH; all 19 archived items were verified again by downloading from the tablet.
- The shipped rm-sync client restored 19 items (99 native files) into a separate
  private home, mount namespace and D-Bus. Every native file matched the backup
  SHA-256. This proves isolated native download compatibility, not production
  migration or complete bidirectional acceptance.
- In a second, isolated one-notebook Drive vault, the shipped client uploaded
  metadata changes and received the generation acknowledgement only after the
  Drive snapshot completed. Its native D-Bus batch finished with result 0.
  Restoring generation 1 created a new UUID, which the client then downloaded.
  A fresh remote download verified both documents and all six native files;
  restored content and ink matched the original fixture byte-for-byte. The
  production library, credentials and synchronization remained unchanged.
- A real preparation-mode uninstall recovered the prior launcher and Quick Share.
  A missing `--system` in the candidate launcher had prevented the native Restart
  action from proceeding after the GUI exited. The launcher now preserves that
  argument, and the installer checks it before any UI restart. A regression test
  rejects a candidate missing the argument without restarting Xochitl.
- Account-wide bulk deletion exposed a global change-feed bottleneck. Polling
  now reads only managed folders, mapped IDs and archive objects. A regression
  simulates 20,000 unrelated deletions and forbids reading the global feed while
  publishing, committing and restoring; nested folders and moves still work.
- The corrected preparation install completed on-device with native `--system`
  present, no service restart loop, and all 19 preview exports completed. Pending
  snapshots and preview exports are reported separately; final queue is zero.
- Retry tests cover bounded exponential backoff with jitter for rate-limit
  rejections, `Retry-After`, cancellation, body replay, and preservation of the
  resumable-upload status check after uncertain writes. Preparation also resumes
  an interrupted first snapshot when no preview exports are yet visible.
- Pinned QMLDiff parses the Cloud page and applies its Settings patch to synthetic
  resources. Fake-tablet tests cover existing XOVI, successful installation and
  uninstall, crashed UI, missing readiness and firmware rejection.

These checks are **not** proof of a completed production migration or full
bidirectional native synchronization.

## Must pass before migration or a stable release

- Actual rm-sync pairing, upload, download, generation conflicts and native
  restoration on firmware 3.27.3.0, using disposable documents.
- Real Drive authorization and upload/download verification, quota exhaustion,
  revoked token, archive damage and reconnect.
- Full migration transaction: current source inventory and restorable backup,
  remote snapshots verified, official credentials/endpoints disconnected, crash
  recovery and conservative rollback. No automatic return to the official cloud
  with unreconciled edits.
- Physical Cloud page readability/interaction, coexistence with Quick Share,
  installation/reuse of XOVI, failed UI rollback and uninstall.
- PDF annotations, EPUB, notebooks, typed text, layers, tags, folder hierarchy,
  trash, history and restore; changes made from Windows and Android Drive.
- Standby, restart, resource consumption and battery behavior.

## Known development limits

The production activation/reconciliation workflow is not yet shipped. Missing
physical validation is deliberately not replaced by successful unit tests.
Active-cloud uninstall is refused until a verified export and reconciliation
workflow exists; preparation-mode uninstall restores the prior UI.
Retention selection is implemented, but automatic pruning remains disabled during
development so that unaccepted migrations cannot delete archive versions.
PDF export compares the native revision before and after rendering; a concurrent
edit defers the export. Its behavior during actual handwriting still needs physical
validation. No production cutover has been performed on the development tablet.
The development owner's personal Google application is in Production and has
been reauthorized. New users must configure their own client; Testing grants
remain unsuitable for unattended operation. Production is not Google verification
or a guarantee against token revocation. Long-duration operation still needs
physical acceptance.
