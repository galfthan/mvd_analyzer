# Dev container

A self-contained dev environment for mvd-analyzer, usable from Zed
(["reopen in container"](https://zed.dev/docs/dev-containers)), VS Code, or
the `devcontainer` CLI. It builds from [`Dockerfile`](Dockerfile) — no
prebuilt image and no parent Makefile required.

## What's in the image

Go (pinned to match the `go.work` toolchain) + `gopls`/`dlv`/`goimports`,
`gh`, `jq`, `git`, `tmux`, `emacs` (go-mode), Claude Code, and the usual
build deps. The web layer is Go→WASM, so Node is intentionally not included.

## Personal config stays out of the repo

The image holds **tools only**. Identity and secrets are injected from your
**host** environment at container-create time via `containerEnv`
(`${localEnv:VAR}` resolves in the environment that launched Zed). Export
these in your host shell profile:

```bash
export GIT_AUTHOR_NAME="Your Name"
export GIT_AUTHOR_EMAIL="you@example.com"
export GH_TOKEN="…"          # optional; or run `gh auth login` in the container
```

Git reads `GIT_AUTHOR_*` / `GIT_COMMITTER_*` from the environment, so your
commit identity works without a committed `.gitconfig`. If a variable is
unset on a contributor's machine it resolves to empty and is simply ignored.

Shell/tmux/emacs preferences are committed as generic defaults
([`tmux.conf`](tmux.conf), [`bashrc.extra`](bashrc.extra),
[`emacs-init.el`](emacs-init.el)) — colours and keybindings, nothing personal.

## UID / GID

The container user is `dev` at UID/GID **1000** (the Linux convention for the
first user, and what bind mounts need so files stay owned by you). This is
the default `ARG` in the `Dockerfile` — no personal values are baked in.

- macOS / Windows (Docker Desktop): UID mapping is handled by the VM; ignore.
- Linux with a non-1000 host UID: bind-mounted files would be owned by the
  wrong id. Override at build time, e.g. add to `devcontainer.json`:

  ```jsonc
  "build": { "args": { "UID": "1001", "GID": "1001" } }
  ```
