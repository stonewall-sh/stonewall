---
title: Qwen Code
slug: qwen
policy: policies/qwen.yml
---

# Stonewall for Qwen Code

Stonewall runs Qwen Code inside a kernel-enforced sandbox. The agent only sees your project directory
and the few binaries and paths the policy names. Everything else on your machine, from `~/.ssh` to your
browser profile, is invisible to it, no matter what a prompt says.

## Policy

For Qwen Code, stonewall.sh provides a specific policy at https://stonewall.sh/policies/qwen.yml. You can
use it by simply including it in your `.stonewall.yml` config, or using the CLI:

```shell
stonewall policy include https://stonewall.sh/policies/qwen.yml
```

It covers only what Qwen Code needs to start and authenticate:

- **Binaries:** `qwen` and `node` (the runtime).
- **Writable paths:** `~/.qwen`, where settings, API keys, OAuth tokens, sessions and trusted folders live.

It intentionally does not allow `sh`, `bash`, `git` or any other tool the agent could use to run
arbitrary programs.

## Related Policies

The Qwen Code policy is meant to be combined with policies for what your project actually needs:

- [base.yml](https://stonewall.sh/policies/base.yml): read-only file inspection (`cat`, `grep`, `ls`, ...), hides `.env` and `secrets`.
- [git-safe.yml](https://stonewall.sh/policies/git-safe.yml): read-only git, no commits, no credentials.
- [git-unsafe.yml](https://stonewall.sh/policies/git-unsafe.yml): full git, including commits and push.
- A language policy for builds and tests, e.g. [go.yml](https://stonewall.sh/policies/go.yml), [python.yml](https://stonewall.sh/policies/python.yml) or [typescript.yml](https://stonewall.sh/policies/typescript.yml).

## Usage

Start the agent through stonewall instead of directly:

```shell
stonewall qwen
```

Or alias it in your bash / zsh profile:

```shell
alias qwen="stonewall qwen"
```
