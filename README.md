# zhongwen-utils

A collection of Chinese reading tools, including an unofficial DuChinese client for reMarkable and PDF readers for desktop and tablet.

## Local PDF support

The laptop-side OCR and page preparation tools live in the
[`pdf-import/` Python subproject](pdf-import/README.md). It contains the tested
PaddleOCR runner, single-pass LLM annotations with audited OCR corrections,
benchmark viewer, and validated single-page assembly. It supports multi-page processing and reading on desktop and reMarkable.

The separate PDF test app lives in `packaging/pdf-test/` and reuses the word
popup and sentence bar in `packaging/shared/` with the DuChinese reader.
Device packaging and deployment commands remain under `scripts/`.

## Goal

Make it possible to read DuChinese stories on a reMarkable using an existing,
paid DuChinese account while preserving the learning interactions that make the
content useful:

- browse the user's DuChinese library
- read stories in a distraction-free, E Ink-friendly interface
- tap a word to reveal its pinyin and meaning
- tap a sentence to reveal its translation
- optionally show or hide pinyin globally
- cache entitled stories for offline reading
- preserve reading progress and saved vocabulary where practical

This is intended to be an independent client, not a redistribution of
DuChinese content.

## Product idea

The project has two deliberately separate parts:

1. **DuChinese adapter**
   - authenticate using the user's own account
   - access the same data made available to that user by the DuChinese web app
   - map stories, chapters, sentences, tokens, pinyin, translations, and
     progress into a stable internal format

2. **reMarkable reader**
   - render the internal story format for E Ink
   - map touch coordinates to words and sentences
   - show lightweight lookup overlays
   - manage navigation, offline storage, and reading progress

Keeping the boundary between these components means changes to DuChinese's web
application should only require changes to the adapter, not to the tablet UI.

```text
DuChinese web app / API
          |
          v
  DuChinese adapter
          |
          v
 stable local story model
          |
          v
   reMarkable reader
```

## Initial MVP

- sign in without storing a plaintext password
- list available stories from the user's account
- download and locally cache one story
- render Chinese text as paginated content
- tap a token to show hanzi, pinyin, and meaning
- tap a sentence to show its translation
- navigate between pages and remember the current position
- work offline after a story has been cached

The UI can initially be developed against synthetic fixtures while the web
adapter is investigated independently.

## Internal story model

The reader should consume a provider-neutral representation similar to:

```json
{
  "storyId": "example-story",
  "title": "Example title",
  "level": "intermediate",
  "sentences": [
    {
      "hanzi": "我今天坐地铁去上班。",
      "translation": "Today I took the subway to work.",
      "tokens": [
        {
          "hanzi": "地铁",
          "pinyin": "dìtiě",
          "meaning": "subway"
        }
      ]
    }
  ]
}
```

The exact model will be refined after inspecting the data exposed by the web
application.

## Constraints

- Users must authenticate with their own legitimate DuChinese account.
- The client must not bypass subscriptions, DRM, paywalls, or other access
  controls.
- DuChinese stories and proprietary assets must not be committed to this
  repository or redistributed.
- Session tokens and cached content are private local data and must be stored
  with restrictive permissions.
- Public test fixtures must be synthetic or otherwise freely licensed.
- The integration is unofficial and may break when DuChinese changes its web
  application or APIs.
- DuChinese is a trademark of its respective owner; this project is not
  affiliated with or endorsed by DuChinese.

## Status

The first end-to-end client is working on reMarkable 2: a native AppLoad QML
frontend talks to a small, statically linked Go backend. It can browse featured
and latest content, search, enter courses, open entitled stories, paginate their
reader data, and reveal pinyin, meanings, and sentence translations.

Authentication currently imports an existing browser session. Passwords never
pass through that browser-session import flow. The optional mobile API helper
reads a password interactively and sends it only to a DuChinese-operated API
over HTTPS; it never stores or prints the password. Session values are not
printed, and both the local and tablet session files are mode `0600`. No
credentials or proprietary DuChinese content are included in the repository.

Offline course caching, reading-position persistence, bookmarks, studied-state
synchronization, and end-of-chapter progress insights are implemented. Still
to do: a first-class browser-assisted login flow, audio playback, and broader
firmware and device compatibility testing.

## Terms and responsible use

This project is licensed under the [MIT License](LICENSE), but that license
applies only to this project's code and documentation. It does not grant any
rights to DuChinese stories, translations, audio, images, trademarks, APIs, or
other service content.

DuChinese's terms were reviewed on 2026-08-15. They reserve rights in service
content and require use of the service within its intended scope. This client
therefore requires each user to supply their own account, requests only content
made available to that account, keeps downloaded material local for personal
use, and does not bypass access controls. Users remain responsible for checking
the current [DuChinese Privacy & Terms](https://duchinese.net/legal) and for
ensuring their use is permitted. The project is unofficial and has not received
approval from DuChinese or Sinamon AB.

## Quick setup on reMarkable 2

This is unofficial software which modifies the running reMarkable UI. Make sure
SSH access works and keep a recovery path before installing community software.
The tested combination is reMarkable 2 software 3.27.3, Xovi 0.3.3, and AppLoad
0.5.3. Other versions may need adjustments.

### Prerequisites

1. Install [Xovi](https://github.com/asivery/xovi) on the tablet.
2. Install AppLoad and its `qt-resource-rebuilder` dependency by following the
   [AppLoad documentation](https://github.com/asivery/rm-appload).
3. On the computer, install Git, OpenSSH, Go, Python 3, and Qt 6's `rcc`
   resource compiler.
4. Connect the rM2 over USB and verify SSH:

   ```sh
   ssh root@10.11.99.1 true
   ```

### Install

First sign in at `https://duchinese.net` in a desktop browser and export an HAR
that includes request data while visiting the library. Treat this file as a
secret. Import only its rotating session cookie:

```sh
scripts/import-session-from-har.py /path/to/duchinese.har
```

The generated file is outside the repository at
`~/.config/duchinese-remarkable/session.json`.

For DuChinese's real end-of-chapter word statistics, create a separate mobile
API session. The password is read interactively and is never stored:

```sh
scripts/login-mobile.py
```

This writes only the mobile device UUID and token to
`~/.config/duchinese-remarkable/mobile-session.json` with mode `0600`.
Transfer both sessions during the first install:

```sh
git clone https://github.com/kenunii/zhongwen-utils.git
cd zhongwen-utils
scripts/install-appload-rm2.sh --with-session --with-mobile-session
```

If the tablet uses an SSH alias or a Wi-Fi address, pass it explicitly:

```sh
scripts/install-appload-rm2.sh remarkable --with-session --with-mobile-session
```

If `rcc` is installed outside `PATH`, point the build at it:

```sh
RCC_BIN=/path/to/qt6/rcc scripts/install-appload-rm2.sh
```

The installer builds and stages the complete package and verifies SHA-256 checksums.
It replaces `/home/root/xovi/exthome/appload/duchinese` only when its backend and
resource bundle are no longer resident. A backend can remain alive after its UI
closes; this is reported as resident, not as proof that the app is open. A resident
package is left untouched and the update remains staged (exit status 3). To fully
quit an older build, open it as a window and long-press AppLoad's **X** control.
New builds terminate their backend through AppLoad when closed. They also expose
a private local control socket: the installer can request a full exit, checking
the backend PID before asking its frontend to call AppLoad's terminate action.
If this request is unsupported or the app stays resident, the update remains staged.

The installer never kills processes or restarts `xochitl`, including during
rollback. It preserves the previous package beside the staging directory.
After installation, close apps and tap **Reload** in AppLoad before opening
DuChinese. Disk installation and runtime activation are reported separately.
Reload reparses the manifest and gives the QML resources a fresh URL prefix.
Unlock the tablet and open AppLoad
from the main UI, and tap **DuChinese**. Browse or search, open an unlocked
story, and tap a Chinese word to show its pinyin and meaning. Tap the translation
prompt to show or hide the selected sentence's translation. Swipe left and right
to change pages; swiping right from the first page goes back, and swiping left
from the last page finishes the chapter. The page count appears briefly when a
chapter opens and after each page change.

Running the installer again updates the existing installation. The session is
left untouched unless `--with-session` or `--with-mobile-session` is passed
explicitly. Finishing a chapter shows DuChinese's real new/learned word counts,
recent progress chart, and word lists when the mobile session is installed.

### Troubleshooting

- No DuChinese entry: confirm that Xovi, `qt-resource-rebuilder`, and AppLoad
  load successfully, then restart `xochitl`.
- SSH fails: enable USB web interface/developer access on the tablet and verify
  that `10.11.99.1` is reachable.
- Build cannot find `rcc`: install Qt 6 development tools or set `RCC_BIN`.
- Login required: export a fresh HAR, rerun the importer, and reinstall. The
  session rotates as it is used; the backend persists replacements atomically.
- To inspect AppLoad startup errors:

  ```sh
  ssh root@10.11.99.1 journalctl -u xochitl -n 100 --no-pager
  ```

## Architecture

The frontend lives in `packaging/appload-native`; the provider adapter and
AppLoad socket client live in `backend`. The native QML frontend renders inside
`xochitl`, so it does not compete with the reMarkable UI for the physical E Ink
framebuffer. The backend sends the DuChinese session only to `duchinese.net`
and accepts reader assets only from `static.duchinese.net`.

Build only the AppLoad package with:

```sh
scripts/build-appload-native.sh
```

The generated `manifest.json` and `resources.rcc` are written to
`build/appload-native`.

## Android and iOS reader

The [shared mobile app](mobile/README.md) reads the same prepared textbook
collection offline, with swiping, pinch zoom, contextual word lookup and saved
reading position. Its source is in `mobile/`; private textbook assets remain
ignored and are exported with `scripts/prepare-mobile-textbook.py`.
