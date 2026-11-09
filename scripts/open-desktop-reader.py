#!/usr/bin/env python3
"""Build and open the prepared textbook as a completely offline desktop app."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
DEFAULT = ROOT / 'build/new-textbook-complete-20261008'
sys.path.insert(0, str(ROOT / 'pdf-import/src'))
from duchinese_pdf.assemble import assemble


def build(collection, output):
    manifest = json.loads((collection / 'manifest.json').read_text())
    pages = []
    output.mkdir(parents=True, exist_ok=True)
    for entry in manifest['pages']:
        data = None
        if entry.get('book'):
            source = json.loads((collection / entry['book']).read_text())
            width, height = source['image_width'], source['image_height']
            if entry['status'] == 'validated':
                book_path = collection / entry['book']
                annotation_dir = (collection / entry['annotation'] if entry.get('annotation')
                                  else book_path.parent / 'annotation')
                characters_path = annotation_dir / 'result/characters.json'
                if characters_path.exists():
                    characters = json.loads(characters_path.read_text())
                    source['words'] = assemble(characters, source, (width, height))['words']
                data = {k: source[k] for k in ('words', 'sentences')}
        else:
            from PIL import Image
            with Image.open(collection / entry['image']) as image:
                width, height = image.size
        pages.append(dict(page=entry['page'], status=entry['status'], width=width,
                          height=height, data=data,
                          image=os.path.relpath(collection / entry['image'], output)))
    book = dict(id=manifest['pdf_sha256'], title=manifest['title'], pages=pages)
    (output / 'book.js').write_text('const book = ' + json.dumps(book, ensure_ascii=False) + ';\n')
    for name in ('index.html', 'reader.js', 'notes.js'):
        shutil.copyfile(ROOT / 'apps/desktop-reader' / name, output / name)
    # Reusing the app window must not pick up a cached version of the reader.
    index = output / 'index.html'
    html = index.read_text()
    for name in ('book.js', 'reader.js', 'notes.js'):
        version = hashlib.sha256((output / name).read_bytes()).hexdigest()[:12]
        html = html.replace(f'src="{name}"', f'src="{name}?v={version}"')
    index.write_text(html)
    return output / 'index.html'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('collection', nargs='?', type=Path, default=DEFAULT)
    parser.add_argument('--build-only', action='store_true')
    parser.add_argument('--pdf', type=Path, default=Path.home()/'Downloads/新当代中文4-课本-compressed.pdf')
    args = parser.parse_args()
    page = build(args.collection.resolve(), ROOT / 'build/desktop-reader')
    if args.build_only:
        print(page)
        return
    url = 'http://127.0.0.1:8767/'
    book_id = json.loads((args.collection/'manifest.json').read_text())['pdf_sha256']
    document_dir = Path.home()/'chinese-docs'
    document_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    output_pdf = document_dir/'Chinese Book.pdf'
    previous_pdf = Path.home()/'Documents/Chinese Book.pdf'
    if not output_pdf.exists() and previous_pdf.exists():
        # Copy atomically, retaining the old PDF and all its annotations.
        before = previous_pdf.stat()
        fd, temporary_name = tempfile.mkstemp(prefix='.chinese-book-', suffix='.pdf', dir=document_dir)
        temporary = Path(temporary_name)
        try:
            with previous_pdf.open('rb') as src, os.fdopen(fd, 'wb') as dst:
                shutil.copyfileobj(src, dst)
            after = previous_pdf.stat()
            if (before.st_ino, before.st_size, before.st_mtime_ns) != (after.st_ino, after.st_size, after.st_mtime_ns):
                raise SystemExit('The existing PDF changed during migration; reopen the reader to retry.')
            def digest(path):
                with path.open('rb') as f: return hashlib.file_digest(f, 'sha256').digest()
            if digest(previous_pdf) != digest(temporary):
                raise SystemExit('The PDF copy could not be verified.')
            try: os.link(temporary, output_pdf)
            except FileExistsError: pass
        finally:
            temporary.unlink(missing_ok=True)
    try:
        with urllib.request.urlopen(url+'api/health', timeout=1) as response:
            health = json.load(response)
            if health['book'] != book_id:
                raise SystemExit('Another textbook is open on reader port 8767.')
            if health.get('version', 1) < 2:
                raise SystemExit('Close the existing reader and its server before reopening to activate the PDF library.')
    except (OSError, ValueError):
        interpreter = Path(os.environ.get('CHINESE_BOOK_PYTHON',
                           ROOT/'apps/desktop-reader/.venv/bin/python'))
        if not interpreter.exists() and not os.environ.get('CHINESE_BOOK_PYTHON'):
            # Reuse the laptop's existing PDF environment when already installed.
            interpreter = ROOT/'build/pdf-quality-check/venv/bin/python'
        if not interpreter.exists():
            raise SystemExit('Install apps/desktop-reader/requirements.txt in '
                             'apps/desktop-reader/.venv, or set CHINESE_BOOK_PYTHON.')
        state = Path.home()/'.local/share/chinese-book'/book_id
        state.mkdir(parents=True, exist_ok=True, mode=0o700)
        with (state/'server.log').open('a') as log:
            process = subprocess.Popen([str(interpreter), str(ROOT/'scripts/desktop-reader-server.py'),
                str(args.collection.resolve()), str(args.pdf.resolve()), str(page.parent),
                str(output_pdf), str(state)],
                stdout=log, stderr=log, start_new_session=True)
        for _ in range(80):
            try:
                with urllib.request.urlopen(url+'api/health', timeout=.2): break
            except OSError:
                if process.poll() is not None: raise SystemExit('PDF server failed; see '+str(state/'server.log'))
                time.sleep(.1)
        else: raise SystemExit('PDF server did not start; see '+str(state/'server.log'))
    browser = shutil.which('chromium') or shutil.which('google-chrome')
    if not browser:
        raise SystemExit('Chromium or Google Chrome is required for the app window.')
    profile = Path.home() / '.local/share/chinese-book/browser'
    profile.mkdir(parents=True, exist_ok=True)
    os.chmod(profile, 0o700)
    # Let Chromium handle scrolling, with a lower input speed for this app only.
    # A runtime Hyprland rule is reapplied whenever the application is launched.
    if os.environ.get('HYPRLAND_INSTANCE_SIGNATURE') and shutil.which('hyprctl'):
        subprocess.run(['hyprctl', 'keyword', 'windowrule',
                        'match:title ^中文课本$, '
                        'scroll_touchpad 0.35, scroll_mouse 0.35'],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.Popen([browser, '--app=' + url, '--class=chinese-book',
                      '--user-data-dir=' + str(profile), '--no-first-run',
                      '--enable-wayland-ime', '--wayland-text-input-version=3',
                      '--no-default-browser-check', '--window-size=1100,950'],
                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                     start_new_session=True)


if __name__ == '__main__':
    main()
