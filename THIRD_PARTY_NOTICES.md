# Licenses and provenance

This is an independent fork of [ddvk/rmfakecloud](https://github.com/ddvk/rmfakecloud),
fixed initially at `3c6de006bdc465bdc0aa14b4de8fac78b5096ee6`. The upstream
copyrights and Git history are retained. The fork and original Drive integration
are distributed under **AGPL-3.0-only**, in [LICENSE](LICENSE).
Original integration: Copyright (c) 2026 Lorenzo Garrone (noire342).
Users interacting with the modified service can obtain its corresponding source
from this repository. Distributors must provide the source for their actual build.

## Removed rendering components

UniPDF, its license modification, rmapi and go-remarkable2pdf are removed from
the current source and module graph. PDF rendering is performed by the user's
existing Xochitl installation. No rights to reMarkable firmware are granted here;
neither original nor modified Xochitl binaries are distributed.

## Quick Share integration helpers

The firmware preparation recipe, UI installation/recovery helper and license
collector derive from [remarkable-quickshare](https://github.com/noire342/remarkable-quickshare),
Copyright (c) 2026 Lorenzo Garrone, MIT. They retain MIT headers and the full
[license](licenses/quickshare-MIT.txt). Firmware string substitutions credit
[rM-self-serve/webinterface-onboot](https://github.com/rM-self-serve/webinterface-onboot),
Copyright (c) 2023 rM-self-serve, [MIT](licenses/webinterface-onboot-MIT.txt).
Quick Share remains a separate MIT project.

## XOVI and the resource rebuilder

The installer reuses compatible XOVI without replacing other extensions. If absent,
it downloads the upstream `pre-v20-08092026` ARM32 archive, SHA-256
`c3e6bfedbd9c3dd844dbf661c848b87e4ec97b21554f91fd361eef3665edeb27`,
verifies it, and installs only XOVI and qt-resource-rebuilder in a dedicated runtime.
Our release packages do **not** embed those upstream native binaries.

- XOVI v0.3.3: `0c8d5269b55c851901d4e4a754dc2d7deab40b17`, **GPL-3.0**.
- rm-xovi-extensions: `6c709dd8416ddf89034831073d76fb4ee3372905`, **GPL-3.0**.
- QMLDiff: `a38cccc4e0de99c0fc7f8cb8bf963a1c234267b1`, **GPL-3.0**.
- uthash/utlist retain Troy D. Hanson's BSD-1-Clause notices.

Full notices, source URLs and component hashes are in `licenses/xovi-provenance.json`
and the accompanying texts. These identify source snapshots; upstream's unlocked
code generator/toolchain do not establish a fully reproducible build. Do not
redistribute their binaries without separately satisfying corresponding-source
requirements. Installed third-party runtimes retain their own provenance.

## Go dependencies

`licenses/go-modules.json` records the exact modules used to build the two shipping
commands. Complete root license, notice and patent texts are copied to
`licenses/go-modules/`, with the Go runtime license alongside them.
`python scripts/collect_licenses.py --check` checks this inventory against the build.
Build/test-only dependencies remain identified by go.mod/go.sum and their sources.

Google, reMarkable and the upstream projects do not endorse this project.
