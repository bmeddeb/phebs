# Release pack inputs (T48.6)

After `make release VERSION=…`, `make release-package VERSION=… RELEASE_KEY=/path/to/key.pem`
signs every template here
for the exact `runtime/phebs` of the release it builds. It then lays out the
default-on package:

```text
<package>/runtime/...             the verified release bundle
<package>/packs/artifacts/*       a copy of artifacts/
<package>/packs/selection/*.json  one signed record per template
```

With no `release_selection.path` configured, that runtime admits the records
against the in-tree trust anchor.

- `artifacts/` holds the 57 published artifacts from
  `v0.2.1-experimental.20261020`, byte for byte, plus the three
  `change-impact-*-1.0.0` artifacts PHEBS-41 added, the three
  `thrift-caller-map-*-1.0.0` artifacts PHEBS-42 added and the three
  `thrift-caller-comparison-*-1.0.0` artifacts PHEBS-44 added on 2026-11-25, the three
  `thrift-operation-impact-*-1.0.0` artifacts PHEBS-45 added on 2026-11-26, and the nine
  `thrift-caller-map-*-1.1.0`, `thrift-caller-comparison-*-1.1.0` and
  `thrift-operation-impact-*-1.1.0` artifacts PHEBS-46 added on 2026-11-26 (the 1.0.0
  artifacts keep their bytes). The records bind their
  digests and the complete census, so never edit, add or remove a file
  without re-qualifying the affected pack.
- `templates/` holds one `PackRelease` template per pack, derived from the
  v0.2.1 records. `release-sign` derives every binding (executable,
  implementation, artifact digests and root) from present fact and ignores
  template digests. Re-signing for a new binary is a rebind, so bump
  `release_version` and `release_id` for each new package. Claims, approvals
  and expiry stay as the owning ticket qualified them.

Validation expiries start on 2027-01-10. After that date a package admits
only the packs whose validation is still current.
