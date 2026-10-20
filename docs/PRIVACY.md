# Privacy and Google Drive access

Last updated: 20 October 2026.

reMarkable Drive Cloud is experimental, self-hosted software. The bridge runs
on your own tablet. You configure your own Google Desktop OAuth client; this
repository does not provide a shared OAuth client or operate a document server.

## Data used by the application

The bridge reads native documents and metadata, including notebook ink, typed
text, layers, tags and folder relationships, to preserve editable documents.
It uploads native objects, immutable snapshots and PDF previews to the dedicated
`reMarkable` folder in the Google Drive account you authorize. It reads files
placed in that folder's inbox to import supported documents. File names, native
UUIDs, Drive file IDs, import receipts and synchronization state are retained
locally and in the application's archive as needed for synchronization and
recovery. The interface also reads your account email address and Drive quota.

The full Drive scope is requested because `drive.file` does not provide access
to arbitrary documents you add directly to the inbox. Application operations
are limited to the dedicated vault and its recorded document IDs; the broad
permission does not change the intended folder boundary. Google enforces the
permissions you grant, so review the source and use a personal OAuth client.

## Where data goes

Documents are transferred directly between the tablet and Google's Drive API
over HTTPS. They are readable in your Drive account without application-level
encryption. Their existing Drive sharing settings and Google's service terms
continue to apply. The project does not receive your documents, run analytics,
serve advertisements, sell personal information or use your data to train AI.

The installer downloads public application and runtime components from their
documented upstream sources. Those requests are separate from document sync.
Private backups and diagnostic files may be stored on the computer you use to
install or troubleshoot the application; they must not be published in Git.

## Credentials, retention and control

Google passwords are entered only into Google's browser sign-in flow. OAuth
tokens and the Desktop client configuration are stored privately on your
computer and tablet; they are never part of the public source or release bundle.
Local control interfaces require a token and bind only to loopback.

You can pause synchronization in Settings and revoke the application's access
in your Google Account. Revocation stops further authorized Drive operations
but does not delete documents, local backups or files already saved in Drive.
During development automatic archive pruning is disabled. Uninstalling the
preparation component preserves credentials, backups and documents; disconnecting
an active cloud requires reconciliation rather than an automatic return to an
older account state. Delete retained copies yourself only after verifying a
usable backup.

## Questions

For a personal OAuth installation, contact the support address displayed on
your own consent screen. General software issues can be reported in
[the public project](https://github.com/noire342/remarkable-drive-cloud/issues).
Never attach tokens, private backups, device passwords or documents to a public
issue.
