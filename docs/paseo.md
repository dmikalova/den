# Paseo

Paseo is the agent orchestrator on den. It runs coding agents (Claude Code and
Gemini CLI) in work trees and serves a web UI. The design is in Notion.

den runs two instances. They use the same image, with separate credentials and
separate state.

| Instance     | URL                                | Local port | Use               |
| ------------ | ---------------------------------- | ---------- | ----------------- |
| `paseo`      | `https://paseo.den.mklv.tech`      | `6767`     | personal projects |
| `paseo-work` | `https://paseo-work.den.mklv.tech` | `6768`     | work projects     |

The local port listens on 127.0.0.1 only. Caddy is the path in. To connect
the app with a direct connection, use the instance host, port `443`, and
"Use SSL". Port 6767 or 6768 from another machine refuses the connection.

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

## Containers

Agents run `docker` and `podman` in the container. This is rootless podman in
the rootless Paseo container. The image side is in the Containers section of
the paseo repo README. The den side is
`mgmt/user/files/containers/systemd/paseo.container.d/20-nested-podman.conf`.
mgmt writes it into the drop-in folder of each instance. It changes the shared
hardening in these ways:

- It adds `/dev/fuse` for `fuse-overlayfs`.
- It adds the default podman capabilities to the bounding set. The image
  entrypoint clears the ambient set, so Paseo and the agents have no effective
  capabilities.
- It turns off `NoNewPrivileges`, so `newuidmap` gets its file capabilities.
- It unmasks `/proc` and turns off the SELinux label.

All of this stays in the user namespace of `dmikalova`. Root in an inner
container is a subordinate ID of `dmikalova` on den. The images are in
`/srv/cache/<instance>/cache/containers`, with no backup. Files there belong
to subordinate IDs. To remove them, use `podman unshare rm -rf <path>`.

## Secrets

A quadlet cannot use `LoadCredential=`, and rootless podman cannot read
`/run/den/secrets`. `den-user-env.service` is a root unit that closes this
gap:

1. It loads the credentials that its unit file names.
2. It writes `/run/den-env/<instance>.env`. The file is on tmpfs, is owned by
   `dmikalova`, and has mode `0600`.
3. The quadlet reads the file with `EnvironmentFile=`.

The values are in `secrets/paseo.sops.json` in the infrastructure repo. `DM_`
is the personal instance and `GS_` is the work instance. The infrastructure
repo copies them into the `den-host` secret:

| Key in `paseo.sops.json`     | Key in `den-host`                    | Variable in the container |
| ---------------------------- | ------------------------------------ | ------------------------- |
| `DM_PASSWORD`                | `PASEO_PASSWORD`                     | `PASEO_PASSWORD`          |
| `DM_CLAUDE_CODE_OAUTH_TOKEN` | `PASEO_CLAUDE_CODE_OAUTH_TOKEN`      | `CLAUDE_CODE_OAUTH_TOKEN` |
| `DM_GH_TOKEN`                | `PASEO_GH_TOKEN`                     | `GH_TOKEN`                |
| `GS_PASSWORD`                | `PASEO_WORK_PASSWORD`                | `PASEO_PASSWORD`          |
| `GS_CLAUDE_CODE_OAUTH_TOKEN` | `PASEO_WORK_CLAUDE_CODE_OAUTH_TOKEN` | `CLAUDE_CODE_OAUTH_TOKEN` |
| `GS_GH_TOKEN`                | `PASEO_WORK_GH_TOKEN`                | `GH_TOKEN`                |

- `PASEO_PASSWORD` is the password for the web UI.
- `CLAUDE_CODE_OAUTH_TOKEN` comes from `claude setup-token`. It uses the
  subscription.
- `GH_TOKEN` is a fine-grained personal access token. Give it access to only
  the repositories that the instance works on. git uses it through
  `gh auth git-credential`.

The commit signing key is a den secret, not a Paseo secret. Other systems can
use the same key. It is the `id_dmikalova` SSH key in `secrets/ssh.sops.json`
in the infrastructure repo. Both instances get the same key:

| Key in `ssh.sops.json` | Key in `den-host`              | Variable in the container            |
| ---------------------- | ------------------------------ | ------------------------------------ |
| `private_key_base64`   | `DMIKALOVA_SSH_KEY`            | `PASEO_SETUP_SIGNING_KEY`            |
| `passphrase`           | `DMIKALOVA_SSH_KEY_PASSPHRASE` | `PASEO_SETUP_SIGNING_KEY_PASSPHRASE` |

- The key is base64, because `den-user-env` refuses a value with a new line.
- The key has a passphrase. The image entrypoint removes the passphrase from a
  copy of the key in `/tmp`, which is tmpfs. The key without a passphrase
  never goes to the disk.
- GitHub shows a commit as verified only if the commit email is a verified
  email of the `dmikalova` account.

To add a key, add it to the `den-host` secret and to `den-user-env.service`.
Then bump the version of `den-user-env-units` in `mgmt/main.mcl`.

### Rotate a token

1. Change the value in `secrets/paseo.sops.json` and apply the infrastructure
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

## Log in to Gemini

The Gemini CLI uses a Google login. The login cannot be automated. Run this
once for each instance from a computer that has SSH access to den:

```sh
ssh -t den podman exec -it -e NO_BROWSER=true paseo gemini
```

For the work instance, use `paseo-work` in place of `paseo`.

1. Select "Sign in with Google".
2. Open the URL in a browser and log in.
3. Paste the code back into the terminal.
4. Type `/quit`.

The credentials are in `~/.gemini` in the agent home, which is backed up.

## Local models

The `local` provider runs Claude Code against Ollama on den. Ollama binds
`127.0.0.1:11434` on den only. In each instance, `Network=pasta:-T,11434`
forwards `127.0.0.1:11434` in the container to that port on den. Ollama serves
the Anthropic messages API, so Claude Code needs no proxy.

- `PASEO_SETUP_LOCAL_URL` sets the Ollama URL.
- `PASEO_SETUP_LOCAL_MODELS` sets the model list. The first model is the
  default.
- The provider has no web search and no web fetch.

To add a model, pull it into Ollama, then add it to `PASEO_SETUP_LOCAL_MODELS`
in both `.container` files:

```sh
podman exec ollama ollama pull <model>
```
