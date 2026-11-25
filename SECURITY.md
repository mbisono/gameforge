# Security policy

## Supported versions

Only the latest release gets fixes. The app can tell you about new versions (About window).

## Reporting a problem

Please do **not** open a public issue. Report it privately on GitHub: **Security → Report a vulnerability** in this repository ([direct link](https://github.com/tschertel/gameforge/security/advisories/new)).

Useful to include: the version, your system, what an attacker could do, and how to reproduce it (for example a crafted file that makes the app crash, write outside the output folder or use too much memory). Never send keys or games.

You get an answer within a few days. Once fixed, the problem is described in the release notes and the changelog, with credit if you want it.

## What the app does with your data

- Keys (`prod.keys`, Wii keys) are read only from the `keys` folder and never sent anywhere.
- The RetroAchievements web API key is stored only in `gameforge.json` and sent only to RetroAchievements.
- Name lists and game databases are downloaded only after the app asks. The new-version check is also off until you agree, and sends only the app's name and version.
