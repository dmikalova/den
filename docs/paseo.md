# Paseo

Paseo is the agent orchestrator on den. It runs coding agents (Claude Code and
Gemini CLI) in work trees and serves a web UI. The design is in Notion.

den runs two instances. They use the same image, with separate credentials and
separate state.

| Instance     | URL                                | Port   | Use                |
| ------------ | ---------------------------------- | ------ | ------------------ |
| `paseo`      | `https://paseo.den.mklv.tech`      | `6767` | personal projects  |
| `paseo-work` | `https://paseo-work.den.mklv.tech` | `6768` | work projects      |

The image is `ghcr.io/dmikalova/paseo`. It is upstream Paseo plus the agent
CLIs and the tools for the standard project check. The
[paseo repo](https://github.com/dmikalova/paseo) builds it.

## Storage

Each instance follows the [storage areas](storage.md). The container uses the
default paths. The bind mounts put each path in the correct area.

| Path in the container          | Path on den                       | Backup |
| ------------------------------ | --------------------------------- | ------ |
| `/home/paseo`                  | `/srv/appdata/<instance>`         | yes    |
| `/home/paseo/.paseo/worktrees` | `/srv/cache/<instance>/worktrees` | no     |
| `/workspace`                   | `/srv/cache/<instance>/workspace` | no     |
| `/home/paseo/.cache`           | `/srv/cache/<instance>/cache`     | no     |
| `/home/paseo/.paseo/models`    | `/srv/cache/models/<instance>`    | no     |

The work trees are not backed up. Agents push often, and den can clone again.

The quadlet maps `dmikalova` to UID 1000 in the container, which is the image
user `paseo`. Thus the files on den are owned by `dmikalova`.

## Secrets

A quadlet cannot use `LoadCredential=`, and rootless podman cannot read
`/run/den/secrets`. `den-user-env.service` is a root unit that closes this
gap:

1. It loads the credentials that its unit file names.
2. It writes `/run/den-env/<instance>.env`. The file is on tmpfs, is owned by
   `dmikalova`, and has mode `0600`.
3. The quadlet reads the file with `EnvironmentFile=`.

The keys are in the `den-host` secret, from `secrets/den.sops.json` in the
infrastructure repo:

| Key in `den-host`                     | Variable in the container |
| ------------------------------------- | ------------------------- |
| `PASEO_PASSWORD`                      | `PASEO_PASSWORD`          |
| `PASEO_CLAUDE_CODE_OAUTH_TOKEN`       | `CLAUDE_CODE_OAUTH_TOKEN` |
| `PASEO_GH_TOKEN`                      | `GH_TOKEN`                |
| `PASEO_WORK_PASSWORD`                 | `PASEO_PASSWORD`          |
| `PASEO_WORK_CLAUDE_CODE_OAUTH_TOKEN`  | `CLAUDE_CODE_OAUTH_TOKEN` |
| `PASEO_WORK_GH_TOKEN`                 | `GH_TOKEN`                |

- `PASEO_PASSWORD` is the password for the web UI.
- `CLAUDE_CODE_OAUTH_TOKEN` comes from `claude setup-token`. It uses the
  subscription.
- `GH_TOKEN` is a fine-grained personal access token. Give it access to only
  the repositories that the instance works on. git uses it through
  `gh auth git-credential`.

To add a key, add it to the `den-host` secret and to `den-user-env.service`.
Then bump the version of `den-user-env-units` in `mgmt/main.mcl`.

### Rotate a token

1. Change the value in `secrets/den.sops.json` and apply the infrastructure
   repo.
2. Fetch the secret again and write the env files:

   ```sh
   sudo systemctl restart den-secrets.service den-user-env.service
   ```

3. Restart the instance:

   ```sh
   systemctl --user restart paseo.service
   ```

## Update the image

Change the `Image=` tag in both `.container` files. Renovate does this later,
when it runs on this repo. `den-reconcile` writes the new quadlet files. Bump
the `quadlets` version in
`mgmt/user/main.mcl` and in `mgmt/user/files/scripts/quadlets.sh`, so that
the instances restart on the new image.

## Not done yet

- Local models: Ollama binds `127.0.0.1:11434` on den. The route from the
  container to it is not set.
