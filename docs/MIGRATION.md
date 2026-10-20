# Native account migration (development)

The normal installer prepares the bridge and Settings page. It does not silently
replace the tablet's account. The experimental device procedure is
`device/activate.sh PRIVATE_BACKUP PRIVATE_EVIDENCE`, run through pinned SSH.
It is restricted to reMarkable 2 firmware 3.27.3.0 and the fixed installation
directory. This is not a stable end-user migration command.

The private evidence must correspond to the exact device and backup manifest.
It records actual native protocol/restore tests, the official cloud inventory,
a stable complete native backup, a verified Drive snapshot and the physical
Settings page check. Successful builds or simulated tests cannot substitute for
these results. Evidence, account tokens and backups must never enter Git.

## Transaction

1. Verify firmware, every RMDOC checksum, archive graph and current library
   hashes; pair a new local account without changing the native account.
2. Stop Quick Share and the native writers. Recheck the stable source and save
   the account configuration, native library (including synchronizer state),
   cache and authentication files in a new private transaction directory.
3. Merge that verified source into the existing Drive vault. Reuse its identity,
   mappings and receipts; commit and download the complete snapshot for checksum
   verification before installing the new account.
4. Atomically replace the native account configuration and add owned service
   drop-ins. Only Xochitl and rm-sync receive private hosts, NSS and CA mounts.
   Global DNS, system trust and the original firmware binary remain unchanged.
5. Start the local bridge, subscribe to the native D-Bus signals before either
   native service can start, and require a successful native batch. Verify the
   original document payloads and unknown metadata fields, then download and
   verify the active snapshot from Drive again.
6. Mark the transaction complete, resume background projections/imports and
   restart Quick Share. Preserve the transaction and all backups.

Native `--system` remains on the GUI command line. During the transition an
owned temporary drop-in disables automatic GUI restart. A separate bounded
watchdog and a boot recovery unit handle interrupted transactions.
The native client can cancel a root request after about 30 seconds. Its complete
pending snapshot remains durable and continues uploading; no early success is
returned. During activation the observer waits for a successful final retry,
which is requested only after the failed batch and completed remote commit.

## Recovery

Before candidate clients have started, recovery restores verified configuration
backups. It never restores an old library over the current documents.

Once candidate clients could have produced edits, recovery keeps the new account
and every document, isolates the native document endpoints on unused loopback,
and displays a recovery state. It does not reconnect the official account.
Damaged transaction journals also fail into this offline state. A later return
to the previous cloud requires exporting and reconciling every intervening edit.

The recovery command is `drive-cloud recover-switch --base
/home/root/drive-cloud --force` on the tablet. It is for an interrupted pending
transaction, not an uninstall or a way to undo a completed migration. Active
uninstall remains blocked until a verified reconciliation workflow is available.

An intact interrupted transaction can be resumed with the same account. Stop
only the bridge and run `drive-cloud settle-switch --base
/home/root/drive-cloud` while the native services remain isolated offline. This
finishes the existing queue and downloads the complete remote snapshot. Only
after that command succeeds, run `sh /home/root/drive-cloud/device/resume.sh`.
The resume gate binds the recent verification to the exact snapshot root and
generation, rechecks the local account and owned drop-ins, and repeats native
batch/payload/remote verification. It never pairs another account or restores an
old document library. A damaged journal requires manual reconciliation.

See [validation](VALIDATION.md) for actual device results and remaining release
gates. No stable release is implied by availability of this procedure.
