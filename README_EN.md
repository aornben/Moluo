# Moluo

[中文](README.md) | English

Moluo is for authors who write long-form fiction with AI. It stores chapters, characters, world settings, plot to-dos, foreshadowing, and source-text evidence in a work workspace, so a new conversation can retrieve the writing materials by work ID.

**Current version 4.9.3-dev · Developer Preview.** We recommend trying it first with a brand-new fictional work. Keep independent backups of real manuscripts, and verify all writes and author decisions yourself.

## Features

- Per-chapter storage, chapter history, and revision rollback with evidence checks.
- A writing pipeline of text preparation, drafting, review, and finalization. Structured data includes character profiles, plot blueprints, to-dos, foreshadowing, facts, cognition, timelines, and state transitions.
- Persistent memory, tiered archiving, and cross-conversation retrieval. A new conversation reads materials back from the workspace; paged results need to be read through.
- Timeline conflict warnings across active, historical, and archived material. For conflicts with clear evidence, an author decision can be recorded, bound to the target version and its hash.
- Independent snapshots, recovery copies, complete work ZIP exports, reading-edition exports, and migration of some legacy formats.

## Architecture & Environment

| Directory | Purpose |
| --- | --- |
| `rikka-mobile-v4-core/` | Go CLI, transactional storage, retrieval, review, diagnostics, and Go tests that generate fictional fixtures |
| `rikka-mobile-v4-final/` | RikkaHub Skill, shell installer, and Python packager |
| `tools/mobile_v4_reference.py` | Desktop reference writer for the historical chapter format, used to build fictional migration fixtures |

The phone target environment is **Android ARM64, RikkaHub 3.3.1, and an initialized Linux arm64 Rootfs**. It requires a dedicated Workspace, the corresponding file tools, and Shell approval. Running the core program on the phone does not require Go or Python. Desktop builds use Go 1.27; the verified toolchain is 1.27.4. The packaging scripts use the Python 3 standard library, verified with Python 3.14. Development-machine builds and CLI smoke tests have been verified on Windows.

## Installation & Getting Started

Get `moluo-v4.9.3-dev-android-arm64.zip` and the `.sha256` file of the same name from the `v4.9.3-dev` pre-release in this repository's **Releases**, and verify the full ZIP following the [installation guide](docs/INSTALL.md). You can also build it yourself on a development machine. The public ZIP comes from a privacy-cleaned source snapshot; installation and upgrade verification were done on a development machine, and Android/RikkaHub on-device verification is still pending.

Import the ZIP into a dedicated novel-writing assistant in RikkaHub. In an isolated fictional Workspace, run the following commands after platform approval.

```sh
sh /skills/moluo-v4-local/install.sh
/workspace/moluo-v4-runtime/bin/moluo version
/workspace/moluo-v4-runtime/bin/moluo init --title 'Fiction: Paper Moon City'
```

Save the `data.workId` returned by `init`. Use it in place of `WORK_ID`.

```sh
/workspace/moluo-v4-runtime/bin/moluo status --work WORK_ID
/workspace/moluo-v4-runtime/bin/moluo doctor --work WORK_ID
/workspace/moluo-v4-runtime/bin/moluo query --work WORK_ID --kind memory
```

For writing, chapter read-back, and author-authorization parameters, see the [Skill command reference](rikka-mobile-v4-final/SKILL.md). For daily workflow and troubleshooting, see [Usage & FAQ](docs/USAGE.md).

## Data & Backups

Current work files live in `/workspace/moluo-v4-works/`, separated by work ID. The CLI protects its write paths with revisions, write locks, content hashes, and transactional commits. Independent snapshots live in `/workspace/moluo-v4-backups/`, and full exports in `/workspace/moluo-v4-exports/`. Snapshots and works may still be on the same device, so regularly save full exports to separate media. Native file tools can edit files directly, so restrict assistant permissions and write through the CLI. Cloud models receive the tool results sent to them; check the scope of use before sending excerpts of your work to a cloud model.

## Verification Status & Known Limitations

- Existing project records include real-device use on Android ARM64/RikkaHub. This round of 4.9.3-dev release-preparation verification was done on desktop. 19/24 is the practical-evaluation caliber; independent, strict verification of all features is not yet complete.
- An existing desktop stress test read back 301 chapters and 1,051,700 Chinese characters. The million-character fictional stress results cover only the corresponding technical scenarios; the full real-world million-character writing workflow is still pending verification.
- Some object-wrapper formats in real legacy libraries still cannot be migrated. Timeline conflicts between active material and historical archives still await dedicated on-device retesting.
- The 30 cross-conversation questions have been answered, but the reference answers have not been independently checked. Sub-items of multi-work isolation, semantic review, crash recovery, and final delivery are still pending verification.
- AI may fabricate, omit records, or make semantic errors. The program can verify submitted source text and structured evidence. Author identity and authorization are still safeguarded by platform approval and assistant permissions.

## v5.0 Plans

Complete legacy-library compatibility and on-device timeline retesting, independently check the cross-conversation answers, and finish verification of multi-work isolation, semantic review, crash recovery, and final delivery. The stable v5.0 release will follow once stability work and verification are complete.

## Feedback & Contributing

You can submit fictional reproductions and redacted logs via GitHub Issues. See [CONTRIBUTING.md](CONTRIBUTING.md) for how to contribute, [SECURITY.md](SECURITY.md) for security issues, and [CHANGELOG.md](CHANGELOG.md) for changes. Dependencies and distribution notices are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
