# Unreleased

Changes merged into `dev` since v1.5.0.

## Features

- `releasenotes`: a service's release notes, from its `docs/changelog` embedded in the
  binary. `Read(fsys, service)` gives what's unreleased (when `unreleased.md` has entries)
  and the newest `vX.Y.Z.md`; `Handler(fsys, service)` serves them as JSON on GET at
  `releasenotes.Path` (`/release-notes`), outside `/api/v1` so nginx doesn't route it:
  services on the internal network read it, not the public. content-manager gathers every
  service's for the dashboard's What's new editor.
