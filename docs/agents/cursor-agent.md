---
title: Cursor CLI
slug: cursor-agent
policy: policies/cursor-agent.yml
---

# Stonewall for Cursor CLI

Stonewall runs Cursor CLI inside a kernel-enforced sandbox. The agent only sees your project directory
and the few binaries and paths the policy names. Everything else on your machine, from `~/.ssh` to your
browser profile, is invisible to it, no matter what a prompt says.

## Policy

For Cursor CLI, stonewall.sh provides a specific policy at https://stonewall.sh/policies/cursor-agent.yml. You can
use it by simply including it in your `.stonewall.yml` config, or using the CLI:

```shell
stonewall policy include https://stonewall.sh/policies/cursor-agent.yml
```

It covers only what Cursor CLI needs to start and authenticate:

- **Binaries:** `cursor-agent` and `agent` (two names for the same launcher), plus `bash`, `basename`, `dirname`, `readlink` and `realpath` (the launcher is a bash script and does not start without them).
- **Readable paths:** `~/.local/share/cursor-agent`, where the installed versions and the bundled Node runtime live.
- **Writable paths:** `~/.cursor`, where settings, MCP config, sessions and login state live.

It intentionally does not allow `git` or any other tool the agent could use to run arbitrary programs
beyond the `bash` the launcher requires.

## Related Policies

The Cursor CLI policy is meant to be combined with policies for what your project actually needs:

- [base.yml](https://stonewall.sh/policies/base.yml): read-only file inspection (`cat`, `grep`, `ls`, ...), hides `.env` and `secrets`.
- [git-safe.yml](https://stonewall.sh/policies/git-safe.yml): read-only git, no commits, no credentials.
- [git-unsafe.yml](https://stonewall.sh/policies/git-unsafe.yml): full git, including commits and push.
- A language policy for builds and tests, e.g. [go.yml](https://stonewall.sh/policies/go.yml), [python.yml](https://stonewall.sh/policies/python.yml) or [typescript.yml](https://stonewall.sh/policies/typescript.yml).

## Usage

Start the agent through stonewall instead of directly:

```shell
stonewall cursor-agent
```

Or alias it in your bash / zsh profile:

```shell
alias cursor-agent="stonewall cursor-agent"
```
