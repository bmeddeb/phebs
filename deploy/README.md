# phebs on Kubernetes

A community-maintained, single-replica deployment of
[phebs](https://github.com/bmeddeb/phebs) (self-hosted code search and static
change-impact analysis in one Go binary) on Kubernetes. Upstream deliberately
ships no official image or manifests until the multi-replica P6 fleet profile,
so this is a working recipe for running today's single-replica phebs on a
cluster you control — not an official image, and not a scale or support
commitment.

The kit was verified on a 3-node linux/amd64 cluster (containerd, Kubernetes
1.35): image build from the v0.3.5 release artifacts, first-run setup,
indexing, and search all checked with `verify.sh`.

## Layout

```
Dockerfile           two-stage build: download + SHA256-verify the official
                     release tarball, add git and a pinned SurrealDB
k8s/00-namespace.yaml
k8s/10-configmap.yaml    phebs runtime config (edit the connections list)
k8s/20-pvc.yaml          20Gi local-path claim (data on node-local disk)
k8s/30-deployment.yaml   single replica + Recreate + two init containers
k8s/40-service.yaml      ClusterIP with a NodePort
verify.sh            in-cluster health/login/version/index/search checks
```

## Build and push the image

The image is linux/amd64 only (the official releases publish only that
architecture) and self-contained: it downloads both release artifacts, checks
their SHA256 checksums, and needs no build context.

```bash
docker build --platform linux/amd64 -t your-registry.example.com/phebs:0.3.5 .
docker push your-registry.example.com/phebs:0.3.5
```

Then point `k8s/30-deployment.yaml` at your registry — the image reference
appears three times (main container and both init containers).

## Install

```bash
kubectl apply -f k8s/
```

On first start phebs prints a one-time setup token in the logs; open the UI,
paste the token, and create your admin account:

```bash
kubectl -n phebs logs deploy/phebs -c phebs | grep 'setup token'
```

The token is single-use and regenerated on every Pod restart; the log line
disappears once an admin exists. There is no password recovery — either keep
using an API key created in Settings, or back up and do a full reset below.

## Access

**NodePort (as shipped)**: `http://<any-node-ip>:30700`. For this to work,
`auth.cookie_secure` is set to `false` — session cookies and API keys travel
in plaintext HTTP, so keep the port inside a trusted network only.

**More secure alternative** — port-forward from your workstation (set
`cookie_secure` back to `true` in the ConfigMap and restart first):

```bash
kubectl -n phebs port-forward svc/phebs 3070:80   # http://127.0.0.1:3070
```

## Day-2 operations

```bash
# Logs / status
kubectl -n phebs logs deploy/phebs -c phebs --tail=50
kubectl -n phebs get pod -o wide

# Configuration changes (ConfigMap changes need a restart to take effect)
kubectl -n phebs edit configmap phebs-config
kubectl -n phebs rollout restart deploy/phebs

# Add/remove repositories: edit the connections list in the ConfigMap and
# restart. Credentials for HTTP(S) Git servers live in Secret phebs-git-auth:
kubectl -n phebs delete secret phebs-git-auth
kubectl -n phebs create secret generic phebs-git-auth \
  --from-literal=username=<user> --from-literal=password=<password-or-token>
# (Drop the whole env block in the Deployment when everything you index is
# publicly reachable.)

# After removing a connection, the repository's data is kept with an orphaned
# marker. To delete it: temporarily set sync.cleanup_orphans: true in the
# ConfigMap → restart → set it back to false → restart.

# Verify (health, login, version and evidence packs, index status; optional
# WAIT_REPO/Q)
./verify.sh
WAIT_REPO=zoekt Q='import' ./verify.sh

# Backup (the database is the only state that cannot be rebuilt; images and
# indexes can)
kubectl -n phebs exec deploy/phebs -c phebs -- \
  /opt/phebs/runtime/phebs backup -config /etc/phebs/phebs.yaml -output /data/backup.tar

# Full reset (wipes accounts and indexes, back to first-run setup; the git
# credentials Secret survives)
kubectl -n phebs delete deploy,pvc phebs phebs-data
kubectl apply -f k8s/
```

## Upgrading to a new release

1. In `Dockerfile`, bump `PHEBS_VERSION` and `PHEBS_SHA256` together (the
   checksum comes from `SHA256SUMS` on the release page); only touch the
   SurrealDB ARGs when its pinned version changed.
2. `docker build --platform linux/amd64 -t your-registry.example.com/phebs:<new> .`
   and `docker push` the same tag.
3. Update the image tag in `k8s/30-deployment.yaml` (three places — the init
   containers reference it too), `kubectl apply`, and wait for the rollout.
   Data directories from older releases are compatible in place; no migration.

## What the two init containers solve (do not remove casually)

1. **copy-config**: phebs refuses a named config file readable by group or
   other (0600). ConfigMap volumes cannot provide that (files are always
   root-owned), so this container copies the config to a private 0600 copy.
2. **clear-stale-runtime**: phebs maintains `/data/.surreal-runtime.json`
   (containing the SurrealDB child PID) to prevent two instances from sharing
   a data directory. Each Pod has its own PID namespace, so after a Pod is
   deleted the stale PID in the leftover file can collide with a live process
   in the new Pod; phebs then misjudges "another live server owns the data
   directory" and crash-loops (reproduced in practice). Removing the file
   before start fixes it. Safety rests on **replicas=1 + Recreate + RWO**
   holding together — if you ever change either of the former, remove this
   cleanup first.

## Constraints and known limitations

- **Single replica**: the data directory is exclusively owned; `replicas`
  cannot be raised, and the service is briefly unavailable during Pod
  recreation. With a local-path volume the Pod is pinned to one node; that
  node going down takes the service with it.
- **Disk watermark**: phebs refuses to create indexes at ≥90% usage of the
  data volume's disk (soft watermark 80%).
- **Buf WIRE contract checks are unavailable in-container**: they need
  bubblewrap, and the default Pod seccomp profile forbids creating user
  namespaces. The WARNING in the logs is expected; everything else works.
- **Evidence packs**: shipped enabled with the official release by default
  (twelve signed records). Verification rounds expire over time — after
  expiry the affected capability goes dark until a newer release re-signs.
  See INSTALL.md in the release package for the dates and for how to disable
  them (`release_selection.package_default: off`, or per-pack revoke).
- **Resources**: the shipped limits are 2 CPU / 4Gi as a starting point. Idle
  usage is far below that; indexing large repositories saturates the limits,
  and insufficient memory fails the index job — size accordingly.
