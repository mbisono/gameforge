# Changelog

All notable changes to GameForge. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/): the minor number grows with new features, the patch number with small fixes.

## [2.9.0] - 2026-11-16

### Added
- **Check RetroAchievements** in the ROMs tab tells, in the status column, whether each ROM, ZIP or 7z is a version RetroAchievements recognizes (with the game's name and number of achievements), from the hash RetroAchievements identifies games by, computed with its rules per console. The game lists come from the RetroAchievements web API, with the user's own API key, kept in `gameforge.json` (`retroachievements_key`) and set in the **Names database ▾** menu; they are cached in `retroachievements.cache`.
- When RetroAchievements has a game but not the version checked, the status says so ("only another version is supported") and the log links to the supported versions.
- **Check RetroAchievements** in the Discs tab does the same for CHD, ISO, CUE and GDI images of PS1, PS2, PSP, Dreamcast, Saturn and Sega CD discs, with the disc hashes of rcheevos (boot executable, PARAM.SFO and EBOOT.BIN, IP.BIN and boot file, or the disc header), reading only those files.
- **Rename…** in the GameCube/Wii → RVZ tab gives ISO, GCM and RVZ files their Redump names, with the Discs tab's databases. RVZ files are listed in the tab (game ID, title and image size from the RVZ header), not converted.
- GameCube games show their banner (`opening.bnr`) next to their name in the GameCube/Wii → RVZ tab, for ISO, GCM and RVZ files; the RVZ reader can read any part of a GameCube RVZ without decompressing the whole disc.
- The 7z reader can unpack files, not only check them.

### Changed
- The **ROMs → 7z** tab is now called **ROMs**, since it also renames and checks; it and the **Discs** tab are the last tabs.
- The downloaded databases (`titledb.cache`, `redump.cache`, `nointro.cache`) are kept in a `cache` folder next to `gameforge.json`; older ones are moved there at startup.
- `gameforge.json` is written readable by the user only (Linux), since it may hold the API key.
- The note in the `keys` folder is now `keys/README.txt`, in English (it was `LEIA-ME.txt`, in Portuguese).

### Fixed
- A ZIP or 7z with a ROM and a note or image (such as a download site's `.txt`) was taken for an arcade set and not renamed; such files inside an archive no longer count.

## [2.8.0] - 2026-11-15

### Added
- **Rename…** in the ROMs → 7z tab gives ROMs, ZIPs and 7z files their No-Intro names (e.g. `Pokemon - Crystal Version (USA, Europe) (Rev 1).gbc`), found by the ROM's CRC-32 and size, read from the archive's header without unpacking. ROMs with an iNES, fwNES, Lynx, Atari 7800 or SNES copier header, and Nintendo 64 `.v64`/`.n64` files, are found too. Archives with several files, such as arcade sets, keep their names; RetroArch files named after the game follow it.
- The No-Intro databases published by libretro are downloaded on demand per console (29 consoles) and cached in `nointro.cache`, with a **Names database ▾** menu in the tab to update them.
- The ROMs → 7z tab lists 7z files too (to rename them; they are not compressed again), and the 7z reader reads the file list of 7-Zip's archives with a compressed header.

### Fixed
- Since 2.7.0, Tk's file and folder dialogs no longer listed the drives (`C:\`, `E:\`…) in the folder menu on Windows, and a folder selected in the folder dialog could need a second click: opening a dialog reloaded Tk's dialog code and undid the app's changes to it.

## [2.7.1] - 2026-11-15

### Changed
- Renaming disc images in the Discs tab also renames the RetroArch files named after the game in the same folder (save states such as `.state.auto` and `.state1`, `.srm` saves, `.png` thumbnails, per-game cheats and options), so they keep working; the preview counts them.
- The About box describes the ROMs → 7z tab and the Redump names, credits their sources and has a clickable link to the project's GitHub page (it is now a window of its own, of fixed size).

### Fixed
- On Linux, message boxes such as About could be resized.

## [2.7.0] - 2026-11-14

### Added
- PSP games show their icon (`ICON0.PNG`) next to their name in the Discs tab, in its own proportions.
- **Names database ▾** menu in the Discs tab, to update the Redump databases of all consoles, with their state (consoles, data date, a hint after 30 days) next to it.
- Tk's file and folder dialogs (Linux, and Windows when the app's language differs from the system's) hide hidden files and folders, with a **Show hidden files and directories** box whose state is remembered (`show_hidden`).
- Each tab keeps its own output folder (`output_dirs`), which follows the folder opened, or the files added or dropped, in that tab.

### Changed
- The **Discs → CHD** tab is now called **Discs**, since it also shows game information and renames.

### Fixed
- In the clam theme (Linux), the selected tab was narrower than the others, with its text lower.
- On Windows, Tk's dialogs listed names starting with a dot and files with the hidden attribute.

## [2.6.0] - 2026-11-14

### Added
- **Rename…** in the Discs tab gives CHD and ISO files their Redump names (e.g. `Ridge Racer (Europe).chd`), found by the product code on the disc; between entries sharing a code, the regions, the revision (GameCube and Wii), the data track size and, if needed, its CRC decide. `.m3u` playlists that list renamed files are updated.
- The Redump databases published by libretro (CC BY-SA 4.0) are downloaded on demand per console and cached in `redump.cache`.

## [2.5.0] - 2026-11-14

### Added
- The Discs tab shows what each disc says about its game: title, product code, version, regions and disc number (PSP `PARAM.SFO`, Dreamcast `IP.BIN`, Saturn and Sega CD headers, PS1/PS2 `SYSTEM.CNF`, GameCube/Wii header).
- CHD files you already have are listed too (read directly, CD, GD-ROM or DVD, made by this app or by `chdman`), with **Check CHD…** on right-click.

## [2.4.0] - 2026-11-14

### Added
- **ROMs → 7z** tab: compresses cartridge ROMs, or recompresses ZIP files such as arcade sets, to 7z, one archive per file, with LZMA at 7-Zip's normal level. Checkboxes choose the extensions; RetroArch files (`.auto`, `.srm`, `.png`…) start unchecked.
- Optional removal of the originals after verifying (to the Recycle Bin on Windows).

## [2.3.1] - 2026-11-13

### Changed
- The log shows file names without their folders, with a blank line between files.
- Each file's status shows how long its conversion took.

## [2.3.0] - 2026-11-13

### Added
- **Rename…** in the Switch tab gives Switch files their standard names (`Name [TitleID][vN]`), with the official title from the file or the game database.

## [2.2.1] - 2026-11-13

### Changed
- The state of the Switch game database is shown in the Switch tab, next to the **Updates and DLCs** menu (formerly "Online"), instead of in the log at startup.

## [2.2.0] - 2026-11-13

### Added
- RVZ block size option (128 KiB to 2 MiB).

### Changed
- RVZ always uses Zstandard's highest level; decompression speed does not depend on it.

## [2.1.0] - 2026-11-13

### Added
- **About…** button with the version.
- The keys bar tells when `prod.keys` is newer than the firmware table the app knows.

### Changed
- CD audio in CHD is compressed with FLAC linear prediction (LPC), as chdman does: 4 to 5 percent smaller on discs with audio tracks.
- Clearer message when an update cannot be applied to the base game, with the solution.

### Removed
- The RVZ → ISO and CSO/ZSO → ISO conversions.

## [2.0.0]

### Added
- First version as GameForge, with tabs for disc images besides Nintendo Switch: conversion to CHD, Xbox XISO, GameCube/Wii RVZ and CSO/ZSO.

## 1.x

nspmerge: merging Nintendo Switch base games, updates and DLCs, with NSZ compression.

[2.9.0]: https://github.com/tschertel/gameforge/releases/tag/v2.9.0
[2.8.0]: https://github.com/tschertel/gameforge/releases/tag/v2.8.0
[2.7.1]: https://github.com/tschertel/gameforge/releases/tag/v2.7.1
[2.7.0]: https://github.com/tschertel/gameforge/releases/tag/v2.7.0
[2.6.0]: https://github.com/tschertel/gameforge/tree/v2.6.0
[2.5.0]: https://github.com/tschertel/gameforge/tree/v2.5.0
[2.4.0]: https://github.com/tschertel/gameforge/tree/v2.4.0
[2.3.1]: https://github.com/tschertel/gameforge/tree/v2.3.1
