# Native Chinese PDF processing

The native PDF three-dot menu exposes **Process Chinese document**. It processes
all pages in the background. A small standalone Go service reads the original PDF,
sends individual pages to Google Vision, then sends recognized Chinese text to
DeepSeek for pinyin, contextual word meanings, and sentence translations. Original
PDFs and handwriting are never edited. Results appear in the existing native
lookup overlay without reloading Xovi.

Google's PDF response supplies word rectangles but can omit character rectangles.
In that case character positions are estimated within each individual OCR word;
rectangles are never spread across a paragraph or visual line break.

Finished pages and OCR results are cached locally by PDF SHA-256. The menu shows
progress, completion, or an error. Tap **Stop Chinese processing** while a job runs;
the current cloud requests are cancelled and the menu changes to **Resume Chinese
processing**. Finished pages and OCR results are reused on resume. The worker
processes up to 10 pages concurrently by default, with at most 28 MB of prepared
PDF page data per batch. Set `-parallel-pages` to 1–20 to change that limit.
Cloud calls are not automatically retried. Only one document processes at a time.
PDF processing currently requires an internet connection; saved lookups work offline.
Limits: 256 MiB per document,
7 MB per extracted PDF page (Google's inline request limit).

The annotation prompt and shared defaults live in
`pdf-import/src/duchinese_pdf/prompts/line_contract.txt` and `line_policy.json`.
Run `python scripts/sync-processor-contract.py` after changing them to regenerate
the tablet's embedded prompt and policy. Installation checks that the generated
files are current. Python and Go run the same alignment examples from
`processor/testdata/annotation_cases.json`. OCR transport, geometry and job
management remain separate: the tablet runs a standalone Go binary and submits
PDF pages, while the desktop uses Python and submits rendered images.

## Build and install

Run `go test -race ./...` in this directory. The synthetic tests make no cloud calls.
The optional `TestLiveCloud` runs only when `PROCESSOR_TEST_CREDENTIALS` and
`PROCESSOR_TEST_PDF` explicitly name a credential file and a one-page Chinese PDF.

Create a private, mode-0600 JSON file **outside version control** with string fields
`vision_api_key` and `deepseek_api_key`. Google Vision must be enabled for its key.
From the repository root:

```sh
scripts/install-pdf-processor.sh rm2 /path/to/private/credentials.json
```

This builds an ARMv7 executable and installs/starts only the independent worker.
It never stops or restarts xochitl or Xovi. The service binds to `127.0.0.1:9876`;
credentials stay in `/home/root/.config/duchinese-pdf-processing/credentials.json`.
Results live in `/home/root/.local/share/duchinese-pdf-processing`.

The native overlay bundle must also be rebuilt and installed, including
`ProcessMenuItem.qml`, `ProcessingClient.js`, and the full Chinese font. Its QMD
patch targets the native `DocumentView`, `Toolbar`, and three-dot `SettingsMenu`.
Validate the patch against resources from the installed firmware before deploying.
Replace RCC files with a same-filesystem rename, keeping the old inode intact.
Activation requires one user-controlled Xovi reload; processing later PDFs does not.

Inspect the worker using `systemctl status duchinese-pdf-processing` and
`journalctl -u duchinese-pdf-processing`. API errors omit credentials and provider
response bodies. The menu can stop and resume the current job without restarting the
service.

## Network access

Both `vision.googleapis.com` and `api.deepseek.com` must be reachable from the
**tablet**, not just the development computer. A network that blocks Google will
prevent new OCR jobs; cached lookups remain available. The Go HTTP client supports
`HTTPS_PROXY` through a systemd service override when a reachable proxy is available.
Do not put proxy credentials or API keys in this repository.

For an automatic, tablet-only AnyConnect connection during processing, see
[the reproducible VPN setup](../packaging/vpn/README.md). A private `vpn` object
enables this mode; cached reads never connect.

The worker runs with low CPU priority and a 192 MiB Go memory target. The unit also
requests a 320 MiB memory limit where the firmware supports memory cgroups and gives
the worker a higher OOM score than ordinary applications. Original PDFs are never
opened for writing, including when processing is interrupted.
