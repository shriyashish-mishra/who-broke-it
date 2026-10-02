# Releasing

A release is a git tag. Everything else is automated or scripted, and every step has a check.

## Checklist

1. **CHANGELOG.** Move "Unreleased" entries under the new version with today's date. Be accurate: list only what is tested. Update the compare links at the bottom.
2. **Version references.** Search for the old version string and update every published example:
   ```bash
   git grep -n "v0\.[0-9]\+\.[0-9]\+" -- README.md docs site action.yml .github
   ```
   (The Action example in the README, the demo repo's workflow, install docs.) Release notes for old versions are history and stay as they are.
3. **Regenerate the site data** if behaviour or demo output changed: `make site-data`, review the diff, commit. Every transcript on the website comes from this.
4. **Run the full gate locally:** `make test && make demo && make demo-team`.
5. **Push `main` and wait for CI** (Linux, macOS and Windows must be green).
6. **Tag and push:**
   ```bash
   git tag -a vX.Y.Z -m "vX.Y.Z: <one line>"
   git push origin vX.Y.Z
   ```
   The `release` workflow runs the tests, builds six archives (Linux, macOS, Windows × amd64/arm64) with `scripts/release.sh`, and publishes them with `checksums.txt` as a GitHub Release.
7. **Verify the published artifacts** the way a stranger would:
   ```bash
   D=$(mktemp -d); curl -fsSL https://raw.githubusercontent.com/shriyashish-mishra/who-broke-it/main/install.sh | WBI_INSTALL_DIR=$D sh && $D/wbi version
   ```
   and the tamper check: edit a copy of `install.sh` so `want=` is wrong; it must refuse to install.
8. **Homebrew tap:** in a checkout of `shriyashish-mishra/homebrew-tap`: `../who-broke-it/scripts/formula.sh vX.Y.Z > Formula/wbi.rb`, commit, push. Then `brew install shriyashish-mishra/tap/wbi && wbi version` and `brew uninstall wbi`.
9. **The Action:** the demo repo (`shriyashish-mishra/wbi-action-demo`) pins `who-broke-it@vX.Y.Z`; bump it and confirm the demo PRs still pass/fail as described in its README.
10. **Website:** pushing `site/**` to `main` deploys it (the `pages` workflow). Check the live URL.

## Mistakes we have already made (so you can skip them)

- v0.1.0 shipped a corrupted branch prefix because a global search-and-replace touched a string literal, and the tests had been rewritten by the same replace. **Do not do blanket renames; assert expected strings as literals in tests.**
- The first installer had no network timeouts. Every `curl` in `install.sh` is now bounded and retried.
- `gofmt` is enforced in CI; run it before pushing.

## Yanking

If a release is broken: edit its notes to say so (`gh release edit vX.Y.Z --prerelease --notes "..."`), cut a fixed patch release, and add the entry to the CHANGELOG. Do not delete tags that people may have pinned.
