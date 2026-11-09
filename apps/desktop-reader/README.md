# Chinese Book

Desktop reader for the prepared Chinese textbook and imported PDFs. Choose a
book from the top-left menu, or use **＋ PDF** or drag a PDF onto the pages to
import it. Imported books live in `~/chinese-docs/books` and open immediately.
Select **Process** to send page images to Google Vision and recognized text to
DeepSeek for Chinese word lookup and translations; this uses cloud services and
their associated charges. Up to 10 pages run in parallel, in batches capped at
approximately 28 MB of encoded OCR images. Set `CHINESE_BOOK_PARALLEL_PAGES`
to 1–20 in the reader server's environment to change the page limit. The
desktop sends page images for OCR, while the reMarkable worker sends PDF pages.
Processing progress and errors appear in the reader. If a page fails, the
current batch finishes and no new batch starts; retry reuses completed pages and
OCR results, but may repeat a request that was interrupted.
Completed books work offline. API credentials must be in the private file
`~/.config/duchinese/pdf-processor-credentials.json` with mode 0600, containing
`vision_api_key` and `deepseek_api_key`.

Scroll normally,
click words for translations, and pinch to zoom just the PDF.

Press **T**, then click the starting point for a textbox. Type with your normal
keyboard or Pinyin input method. Text saves automatically after a short pause;
click elsewhere, press Escape, or Ctrl+Enter to finish. Click existing text to
edit it. Unfocused textboxes are clickable only on their visible text; blank
space passes clicks through to the textbook. **Alt-click** a textbox to delete
it; **Ctrl-drag** to move it within the page. Both changes save automatically
to the PDF. While editing, drag the handle on the right edge to change its
width, or press **Ctrl+Shift+Left/Right**. Width changes also save automatically.
Clearing the text also
removes it from the saved PDF. Empty or whitespace-only textboxes disappear
when they lose focus. No toolbar is added.

The original textbook is preserved. Text is saved as editable PDF FreeText
annotations in `~/chinese-docs/Chinese Book.pdf`. An existing copy in
`~/Documents/Chinese Book.pdf` is copied there the first time the reader starts.
Imported books have their own `annotated.pdf` alongside their source and data.
Textbox state and recovery drafts
are also kept locally. The copy is created on the first nonempty textbox save.

```sh
python -m venv apps/desktop-reader/.venv
apps/desktop-reader/.venv/bin/pip install -r apps/desktop-reader/requirements.txt
python scripts/open-desktop-reader.py
```

Requires Chromium and Python with PyMuPDF and Pillow. `CHINESE_BOOK_PYTHON` can
select another interpreter; the laptop's existing PDF environment is also
reused when available. The default collection is
`build/new-textbook-complete-20261008`; its matching source is
`~/Downloads/新当代中文4-课本-compressed.pdf`. Pass a different prepared
collection and `--pdf /path/to/matching.pdf` together when needed.

A local server binds only to `127.0.0.1:8767` for writing PDF annotations. No OCR,
translation API or internet connection is used. Source PDFs are hash checked;
PDF and textbox writes use atomic replacement. Only nearby page rasters and
word overlays are loaded. Reading position and zoom persist per book in browser
storage and an atomic local `reading-position.json` file. Closing the window flushes the latest
position; reopening restores the exact page, offset and zoom. On this
Hyprland laptop, the launcher applies an app-only scroll factor of 0.35.
The installed shortcut is `~/.local/share/applications/chinese-book.desktop`.
