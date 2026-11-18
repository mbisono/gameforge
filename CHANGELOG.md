# Changelog

All notable changes to GameForge. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/): the minor number grows with new features, the patch number with small fixes.

## [2.14.0] - 2026-11-18

### Added
- **Check ▾ → Sets for FBNeo** (and **Sets for MAME 2000**, **2003**, **2003-Plus**, **2010**, **2015** and **2016**, the MAME cores of RetroArch, and **Sets for Flycast (Naomi, Atomiswave)**) in the ROMs tab checks the checked ZIPs and 7z files as arcade sets of that emulator, with its list of sets, downloaded from GitHub the first time it is used (FBNeo's own list, about 14 MB; for the MAME cores, the lists libretro keeps for them, from 2 to 53 MB): whether the list has the set's short name, and whether all its files are there with the right CRCs, in the set itself or in its parent and BIOS sets in the same folder (non-merged, split and merged sets all work). The status column shows `✓ FBNeo: Blazing Star`, or what is wrong: files missing, files with another CRC (usually a set made for another MAME version), or a clone's parent set the folder lacks (`needs blazstar.zip in this folder`). The CHDs a set needs (MAME's disks, the GD-ROMs of Naomi games) are looked for where the emulators look, `set/name.chd` next to the archive, and a missing one counts as missing. Flycast has no list of sets: its table of games (in its source, `naomi_roms.cpp`, about 300 KB) is downloaded and read as data. Only the folder of the ROMs is looked at, and no BIOS is needed: a BIOS set (`neogeo.zip`, `naomi.zip`, `awbios.zip`) in the folder is checked too, and one that is not there is only noted in the log; the game's own files decide. Sets the emulator marks as not working well and archives whose name is unknown but whose files are a known set are pointed out. Only the archives' headers are read; nothing is changed or rebuilt. **Names database ▾** updates the lists already downloaded.

- **Check ▾ → Duplicates: check only the extra copies** in the ROMs tab checks the extra copies found by the last duplicate check at any time, not only when the check offers it, and unchecks all the other files; it runs the check first if there was none or the list changed.

### Changed
- The ROMs tab gathers its checks in one **Check ▾** menu (RetroAchievements, Duplicates, Sets for FBNeo, the MAME cores and Flycast), which keeps its row of buttons narrow enough for a 1366-pixel screen.

## [2.13.1] - 2026-11-17

### Fixed
- On the first run on Linux (seen on KDE Plasma with Wayland), the question whether to check for new versions did not respond: **Yes** and **No** did nothing and the window could not be closed. The question was opened before the window manager had shown the main window; it now waits until the main window is on screen.

## [2.13.0] - 2026-11-17

### Changed
- The **About…** window has an **Updates** box, as in Rufus: a list chooses how often the app checks for a new version (Never, Daily, the default, Weekly or Monthly), and **Check now** asks GitHub at once, even with the automatic check off, and tells whether a newer version is out, this one is the newest, or the check failed. The box also shows when the last check was made.

## [2.12.0] - 2026-11-17

### Added
- **Find duplicates** in the ROMs tab finds the checked ROMs, ZIPs and 7z files that hold the same game, such as a Neo Geo game as a `.neo`, inside a `.7z` and as MAME's `.zip` set. Files count as the same game when the No-Intro database gives them the same name (by the ROM's CRC), when they hold the same ROM, or when they have the same name; MAME's Neo Geo sets are found by their short names (`blazstar.zip` is Blazing Star). The status column tells which file of each game stays and which are extra copies, and the log lists each group with the files' sizes and how much space the extra copies take. The app then offers to check only the extra copies. The file that stays is the first format in an order set in the tab (by default `7z, neo, zip`: a 7z with the `.neo` inside, then a loose `.neo`, then MAME's ZIP), then the one that already has the database name, then the largest.
- **Delete checked…** in the ROMs tab deletes the checked files, after showing them and asking: to the Recycle Bin on Windows, for good on Linux and macOS. RetroArch saves and states are left alone.
- Experimental macOS builds, for Apple Silicon and Intel Macs, compiled from the same code but not tested on a Mac. They are not signed by Apple; the README explains how to open them.

## [2.11.1] - 2026-11-17

### Fixed
- **Rename…** in the Nintendo Switch tab renamed every file in the list, checked or not; it now renames only the checked files. The same goes for **Rename…** and **Check RetroAchievements** in the Discs, GameCube/Wii → RVZ and ROMs tabs, and for the online check with the game database, which looks only at games with a checked file. When none of the files an action works on is checked, the app says so.

### Changed
- CHDs in the Discs tab, RVZs in the RVZ tab and 7z files in the ROMs tab, which are listed to be renamed or checked but not converted, now have a check box like the other files, checked at first.
- The file lists scroll sideways: each column is at least as wide as its longest text, and a horizontal scroll bar shows what does not fit in the window.

## [2.11.0] - 2026-11-17

### Added
- **Rename…** in the ROMs tab also renames Neo Geo games in the single-file `.neo` format (NeoSD, MiSTer, Geolith), loose or in a ZIP or 7z, with the names of libretro's own Neo Geo list (`dat/SNK - Neo Geo.dat`), downloaded on demand like the No-Intro lists: `sonicwi2.neo` → `Aero Fighters 2 - Sonic Wings 2.neo`. Western and Japanese titles are joined by " - ". MAME and FBNeo zip sets still keep their short names.
- **Files at a time** in the ROMs tab compresses several files at once (1 by default; half the processor's threads, up to 4, are recommended). The progress bar shows the whole batch, and error lines name their file.
- The app can check once a day on GitHub whether a newer version is out. It asks the first time it starts; when there is a newer version, a link to it appears at the top of the window. Only the app's name and version are sent, and nothing is downloaded or installed. The check can be turned on or off in **About…**.

### Changed
- The databases are downloaded from the root of libretro-database, since the Neo Geo list is in another folder than the No-Intro and Redump lists.

## [2.10.0] - 2026-11-16

### Added
- Nintendo DS ROMs show their icon (the 32 × 32 picture of the DS menu, from the ROM's banner) next to their name in the ROMs tab, loose or in a ZIP or 7z; in a 7z only the start of the ROM is decompressed, in the background, so the list appears at once and the icons follow.
- Dreamcast games in a CHD or GDI show their disc label (`0GDTEX.PVR`, the picture printed on the disc) in the Discs tab, when the disc has one; the app reads the PVR texture formats used for it (twiddled, VQ-compressed or plain, with or without mipmaps).
- Xbox games show their title image (from `default.xbe`, in DXT or plain texture formats) in the Xbox → XISO tab.

### Changed
- In lists with icons, every icon takes the width of the widest one and rows without an icon keep the same space, so the names line up (for example Wii rows next to GameCube banners, or PSP icons next to Dreamcast labels).
- The README notes that the disc hashes of the RetroAchievements check were confirmed with real Dreamcast, PS1 and PSP games.

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

[2.14.0]: https://github.com/tschertel/gameforge/releases/tag/v2.14.0
[2.13.1]: https://github.com/tschertel/gameforge/releases/tag/v2.13.1
[2.13.0]: https://github.com/tschertel/gameforge/releases/tag/v2.13.0
[2.12.0]: https://github.com/tschertel/gameforge/releases/tag/v2.12.0
[2.11.1]: https://github.com/tschertel/gameforge/releases/tag/v2.11.1
[2.11.0]: https://github.com/tschertel/gameforge/releases/tag/v2.11.0
[2.10.0]: https://github.com/tschertel/gameforge/releases/tag/v2.10.0
[2.9.0]: https://github.com/tschertel/gameforge/releases/tag/v2.9.0
[2.8.0]: https://github.com/tschertel/gameforge/releases/tag/v2.8.0
[2.7.1]: https://github.com/tschertel/gameforge/releases/tag/v2.7.1
[2.7.0]: https://github.com/tschertel/gameforge/releases/tag/v2.7.0
[2.6.0]: https://github.com/tschertel/gameforge/tree/v2.6.0
[2.5.0]: https://github.com/tschertel/gameforge/tree/v2.5.0
[2.4.0]: https://github.com/tschertel/gameforge/tree/v2.4.0
[2.3.1]: https://github.com/tschertel/gameforge/tree/v2.3.1
