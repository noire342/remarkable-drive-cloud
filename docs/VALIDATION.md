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
- ARMv7 tablet and Windows/macOS/Linux amd64/arm64 setup binaries built.
- Pinned QMLDiff parses the Cloud page and applies its Settings patch to synthetic
  resources. Fake-tablet tests cover existing XOVI, successful installation and
  uninstall, crashed UI, missing readiness and firmware rejection.

These backup checks are **not** proof of native synchronization or physical restore.

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
Drive change-token expiry currently suspends synchronization and requires repair;
it never interprets an unavailable remote archive as an empty library.
PDF export consistency during simultaneous handwriting still requires physical
validation. No production cutover has been performed on the development tablet.
The personal Google application currently remains in Testing. Its refresh token
is temporary (Google's seven-day test-app rule); complete production branding and
reauthorize before relying on unattended continuous operation.
