## What and why

<!-- The behaviour change a user would notice, and the problem it solves. -->

## Checks

- [ ] `make test` passes (it runs `go vet` and the whole suite)
- [ ] `gofmt -l .` prints nothing
- [ ] New behaviour has a test that fails without the change (mutation-check it if it guards a safety property)
- [ ] User-facing change is in `README.md` / `docs/` and `CHANGELOG.md` (under "Unreleased")
- [ ] Protocol or file-format change: `docs/PROTOCOL.md` updated, and old data still loads
- [ ] If this touches an adapter: its status in `docs/ADAPTERS.md` is still honest (nothing marked verified unless a real agent ran)
- [ ] If this changes demo output: `make site-data` re-run and `site/data.js` committed

## Verification notes

<!-- How you checked it for real: commands you ran, platforms, anything you could NOT test. -->
