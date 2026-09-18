<div align="center">

<img src="docs/logo.svg" alt="Stonewall" width="128" height="128">

# stonewall<span style="opacity:.45">.sh</span>

**Kernel-enforced sandbox for AI coding agents**

[![Build](https://img.shields.io/github/actions/workflow/status/stonewall-sh/stonewall/build.yml?branch=main&style=flat-square&logo=githubactions&logoColor=white&label=build)](https://github.com/stonewall-sh/stonewall/actions/workflows/build.yml)
[![Release](https://img.shields.io/github/v/release/stonewall-sh/stonewall?style=flat-square&logo=github&label=release)](https://github.com/stonewall-sh/stonewall/releases/latest)
[![OpenSSF Scorecard](https://img.shields.io/ossf-scorecard/github.com/stonewall-sh/stonewall?style=flat-square&logo=openssf&label=scorecard)](https://scorecard.dev/viewer/?uri=github.com/stonewall-sh/stonewall)
[![Security Rating](https://sonarcloud.io/api/project_badges/measure?project=stonewall-sh_stonewall&metric=security_rating)](https://sonarcloud.io/project/overview?id=stonewall-sh_stonewall)
[![License: MIT](https://img.shields.io/badge/License-MIT-E4572E.svg?style=flat-square)](https://github.com/stonewall-sh/stonewall/blob/main/LICENSE)

Available on:

[![Linux](https://img.shields.io/badge/Linux-bubblewrap-FCC624?style=flat-square&logo=linux&logoColor=black)](https://github.com/containers/bubblewrap)
[![macOS](https://img.shields.io/badge/macOS-sandbox--exec-000000?style=flat-square&logo=apple&logoColor=white)](https://keith.github.io/xcode-man-pages/sandbox-exec.1.html)

</div>

---

## 🧱 What it is

> AI, like everything else, can and should not “secure itself”. Most agentic security harnesses are a joke. Likewise, 
> prompts, plugins, and markdown files don’t provide real security. They are best-effort recommendations that agents 
> can hallucinate about, or even ignore in the worst case.

Stonewall is a local, kernel-enforced **sandbox** for AI **coding agents**. It enforces access to files and tools
independently of agent prompts or configuration, based on **configurable policies**:

- Whitelist tools and binaries that can be used
- Hide or lock project files like `.git` or `README.md`
- Block files outside the project by default
- Expose host paths, i.e. `~/.cache` explicitly
- Keep stonewall policies inaccessible to agents, to mitigate privilege escalation
- Environment variable restriction *(planned)*
- Network isolation *(planned)*

## ⚡ Usage

```shell
stonewall [options] <agent> [arguments]
```

Basically, stonewall runs any coding agent (i.e. `claude`, `codex` or `qwen`) in a local sandbox. Options go before the
agent. Everything after the agent name is passed to it untouched. Usage is as simple as:

```shell
stonewall claude
```

## 📦 Installation

Simple install the latest release for your platform using the installer:

```
curl -fsSL https://stonewall.sh/install.sh | sh
```

*The script is [short](https://github.com/stonewall-sh/stonewall/blob/main/install.sh), read it first.*

Alternatively, download the binary for your platform from the
[latest release](https://github.com/stonewall-sh/stonewall/releases/latest), or build from source with Go 1.27 or newer:

```
go install github.com/stonewall-sh/stonewall/v2@latest
```

## 📜 Policy

Stonewall works based on a policy called `.stonewall.yml`, at your project root. It is automatically created on first
run and contains rules for binaries, paths and project directories being accessible from within the sandbox. See the 
following example:

```yaml
include:
  - https://stonewall.sh/policies/base.yml  # remote policy include, see below
  - ~/.stonewall/policies/corporate.yml     # local policy include
project:
  hidden:
    - .env.local        # local override for a hidden project file
  writable:
    - .git
bin:
  allowed:
    - sh                # whitelist of allowed binaries
    - git
    - npm
    - npx
expose:
  write:
    - ~/.npm            # directories outside the project that should be accessible
```

### Include Policies

Every policy can include other policies, using an `include` property. Included policies can be local file paths or
remote URLs. Stonewall.sh provides [official remote policies](https://stonewall.sh/policies). Keep in mind that they
come without any warranties. The CLI will ask you to review remote policies before using them.

You can use the interactive policy picker to include official policies:

```bash
stonewall policy pick
```

To manage any other remote policy, you can use the following:

```bash
stonewall policy include https://stonewall.sh/policies/claude.yml # include a remote policy
stonewall policy update # updates & cache remote policies
stonewall policy --help # show help for all policy commands
```

Additionally use the CLI to scaffold your own include policies:

`stonewall policy create`

## ⚙️ How it Works

Inside the sandbox:

| Path                         | Linux (`bubblewrap`)                     | macOS (`sandbox-exec`)          |
|------------------------------|------------------------------------------|---------------------------------|
| Project                      | read-write                               | read-write                      |
| `project.readonly` entries   | mounted read-only                        | writes denied                   |
| `project.hidden` directories | appear empty                             | reads and writes denied         |
| `project.hidden` files       | read as empty                            | reads and writes denied         |
| `$HOME`                      | empty, except `expose` entries           | denied, except `expose` entries |
| `expose.read` entries        | read-only bind                           | reads allowed, writes denied    |
| `/usr`, `/etc`, `/opt`, …    | read-only                                | unchanged, read-write           |
| `PATH`                       | a directory of allowlisted programs only | the same directory              |
| Executing binaries           | kernel-denied unless in `bin.allowed`    | kernel-denied unless in `bin.allowed` |
| Network                      | shared with the host                     | shared with the host            |

Linux makes hidden content vanish. macOS still lists the names but denies every access. Both stop the agent from reading
the secret.

Linux builds the sandbox from nothing and mounts in only what is listed. macOS starts from the full system and denies
your home directory, so locations such as `/opt/homebrew` stay writable there.

Exec itself is kernel-enforced, not just `PATH`: a binary outside `bin.allowed` is denied even by absolute path. An
allowlisted interpreter such as `bash` or `python` can still run anything given to it, though.

**Known limitations**

- Environment variables pass through unchanged, except `SSH_AUTH_SOCK`. Secrets in your shell environment are visible to
  the agent.
- Linux: `expose.write` single files such as `~/.claude.json` are mount points. Tools that rewrite them by rename fail
  with `EBUSY`.
- Linux: the agent keeps the controlling terminal so job control works. On kernels before 6.2 that leaves `TIOCSTI`
  input injection open.
- Tools that rewrite an `expose.read` file, such as `git config --global`, fail.
- Not yet: network isolation, hiding file names on macOS.

## 🛠️ Development

```
make build       # ./stonewall
make test        # unit tests
make e2e         # end-to-end on this machine, macOS or Linux
make e2e-linux   # same, inside Docker with bubblewrap (runs --privileged)
make site        # website preview at http://localhost:1313
```

`main.go` is the CLI. `internal/policy` finds the root, loads and merges a policy with its includes, and scaffolds one.
`internal/sandbox` resolves a policy into a `Plan` of absolute host paths and renders it as `bwrap` arguments or a
Seatbelt profile. Both renderers are pure functions with golden tests. `test/e2e.sh` runs the same assertions on both
platforms.

`site/` is the Hugo project behind stonewall.sh. It mounts the README and `policies/` from the repository root, so the
policy pages and `policies/_index.yml` are generated from the yml files; nothing is copied.

`--dry-run` shows exactly what the backend receives. Start there when something is unexpectedly blocked or visible.

**Release.** CI runs gofmt, `go vet`, the unit and end-to-end tests on Ubuntu and macOS, and a snapshot cross-build on
every push. To release, tag and push:

```
git tag v2.1.0 && git push origin v2.1.0
```

The same checks run again, then GoReleaser builds Linux and macOS binaries for amd64 and arm64 and publishes them with
checksums as a GitHub release. The tag is the only version source: it is stamped into the release binaries and reported
by `go install` builds. A local `make build` says `dev`. A new major version needs the module path suffix bumped, for
example `stonewall/v2` to `stonewall/v3`, in `go.mod` and the imports.

## 🤝 Contributing

Issues and pull requests are very welcome, be it new / updated policies or actual CLI features.

## ⚖️ License

Released under the [MIT License](https://github.com/stonewall-sh/stonewall/blob/main/LICENSE).
