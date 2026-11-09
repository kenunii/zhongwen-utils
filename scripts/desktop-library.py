#!/usr/bin/env python3
"""Private desktop PDF library and background Google Vision processing."""
import hashlib
import fcntl
from concurrent.futures import ThreadPoolExecutor, as_completed
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time
import urllib.request
import socket

import pymupdf

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'pdf-import/src'))
from duchinese_pdf.google_vision import call_vision, convert_response
from duchinese_pdf.batch import process_page
from duchinese_pdf.annotate import run as annotate
from duchinese_pdf.assemble import assemble
from duchinese_pdf.prepare import write_json
from duchinese_pdf.policy import POLICY

LIBRARY = Path(os.environ.get('CHINESE_BOOK_LIBRARY', Path.home()/'chinese-docs/books'))
KEYS = Path(os.environ.get('CHINESE_BOOK_CREDENTIALS', Path.home()/'.config/duchinese/pdf-processor-credentials.json'))
ID = re.compile(r'^[0-9a-f]{64}$')
DEFAULT_PARALLEL_PAGES = POLICY['default_parallel_pages']
MAX_BATCH_BYTES = POLICY['max_batch_bytes']


def atomic(path, data):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, name = tempfile.mkstemp(dir=path.parent, prefix='.tmp-')
    try:
        with os.fdopen(fd, 'w') as out: json.dump(data, out, ensure_ascii=False)
        os.replace(name, path)
    finally:
        Path(name).unlink(missing_ok=True)


def read(path, default=None):
    try: return json.loads(path.read_text())
    except (FileNotFoundError, ValueError): return default


def book_path(book_id):
    if not ID.fullmatch(book_id): raise ValueError('Invalid book ID')
    return LIBRARY / book_id


def catalog():
    if not LIBRARY.exists(): return []
    return [meta for p in sorted(LIBRARY.glob('*/book.json')) if (meta := read(p))]


def build(collection, frontend):
    spec = importlib.util.spec_from_file_location('desktop_launcher', ROOT/'scripts/open-desktop-reader.py')
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    module.build(collection, frontend)


def import_pdf(data, name):
    if not data or len(data) > 256*1024*1024: raise ValueError('PDF must be at most 256 MiB')
    try:
        with pymupdf.open(stream=data, filetype='pdf') as doc:
            if doc.needs_pass or not doc.page_count: raise ValueError('PDF is locked or empty')
            count = doc.page_count
    except (RuntimeError, pymupdf.FileDataError): raise ValueError('Invalid PDF') from None
    digest = hashlib.sha256(data).hexdigest(); folder = book_path(digest)
    if (folder/'book.json').exists(): return read(folder/'book.json')
    folder.mkdir(parents=True, mode=0o700, exist_ok=True)
    name = Path(name).name[:200] or 'Imported PDF'
    title = Path(name).stem or 'Imported PDF'
    (folder/'source.pdf').write_bytes(data)
    collection = folder/'collection'; images = collection/'images'
    images.mkdir(parents=True, mode=0o700, exist_ok=True)
    entries = []
    with pymupdf.open(folder/'source.pdf') as doc:
        for i, page in enumerate(doc):
            image = images/f'page-{i+1:03}.png'
            page.get_pixmap(matrix=pymupdf.Matrix(2,2), alpha=False).save(image)
            entries.append({'page':i+1,'image':f'images/{image.name}','status':'image-only'})
    atomic(collection/'manifest.json', {'pdf_sha256':digest,'title':title,'pages':entries})
    build(collection, folder/'frontend')
    meta = {'id':digest,'title':title,'pages':count}
    atomic(folder/'book.json', meta)
    atomic(folder/'job.json', {'state':'idle','done':0,'total':count,'message':'Ready to process'})
    return meta


def job(book_id):
    folder = book_path(book_id)
    status = read(folder/'job.json', {'state':'idle'})
    pid = status.get('pid')
    if status.get('state') == 'running' and pid:
        try: os.kill(pid, 0)
        except ProcessLookupError:
            status.update(state='error',message='Processing was interrupted; inspect the saved results before retrying')
            atomic(folder/'job.json',status)
    return status


def open_book(book_id):
    folder = book_path(book_id)
    if not (folder/'book.json').exists(): raise ValueError('Book not found')
    with (folder/'.server-lock').open('w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        saved = read(folder/'server.json', {})
        port = saved.get('port')
        if type(port) is int and 1024 <= port <= 65535:
            try:
                with urllib.request.urlopen(f'http://127.0.0.1:{port}/api/health',timeout=.5) as response:
                    if json.load(response).get('book') == book_id: return port
            except (OSError, ValueError): pass
        with socket.socket() as sock:
            try: sock.bind(('127.0.0.1', port if type(port) is int and 1024 <= port <= 65535 else 0))
            except OSError: sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        state=folder/'state'; state.mkdir(mode=0o700, exist_ok=True)
        log=(folder/'server.log').open('a')
        try:
            process=subprocess.Popen([sys.executable,str(ROOT/'scripts/desktop-reader-server.py'),
                str(folder/'collection'),str(folder/'source.pdf'),str(folder/'frontend'),
                str(folder/'annotated.pdf'),str(state),'--port',str(port)],
                stdout=log,stderr=log,start_new_session=True)
        finally: log.close()
        for _ in range(80):
            try:
                with urllib.request.urlopen(f'http://127.0.0.1:{port}/api/health',timeout=.2) as response:
                    if json.load(response).get('book') == book_id:
                        atomic(folder/'server.json',{'port':port}); return port
            except (OSError, ValueError): pass
            if process.poll() is not None: break
            time.sleep(.1)
        raise ValueError('Could not start book reader')


def start(book_id):
    folder = book_path(book_id)
    if not (folder/'book.json').exists(): raise ValueError('Book not found')
    with (folder/'.job-lock').open('w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        return start_locked(book_id, folder)


def start_locked(book_id, folder):
    current = job(book_id)
    if current.get('state') == 'running': raise ValueError('Processing is already running')
    if current.get('state') == 'completed': raise ValueError('This book is already processed')
    if KEYS.stat().st_mode & 0o077: raise ValueError('Credentials must be private (mode 0600)')
    keys = read(KEYS, {})
    if not keys.get('vision_api_key') or not keys.get('deepseek_api_key'): raise ValueError('Cloud credentials are missing')
    parallel_pages()
    atomic(folder/'job.json', {'state':'running','done':0,'total':read(folder/'book.json')['pages'],'message':'Starting'})
    log = (folder/'processing.log').open('a')
    try:
        process = subprocess.Popen([sys.executable, __file__, '--worker', book_id], stdout=log, stderr=log, start_new_session=True)
        status = read(folder/'job.json', {})
        if status.get('state') == 'running' and not status.get('pid'):
            status['pid'] = process.pid; atomic(folder/'job.json', status)
    except Exception:
        atomic(folder/'job.json', {'state':'error','message':'Could not start processing'})
        raise
    finally: log.close()


def parallel_pages():
    try: parallel = int(os.environ.get('CHINESE_BOOK_PARALLEL_PAGES', DEFAULT_PARALLEL_PAGES))
    except ValueError: raise ValueError(f"CHINESE_BOOK_PARALLEL_PAGES must be between 1 and {POLICY['max_parallel_pages']}") from None
    if not 1 <= parallel <= POLICY['max_parallel_pages']: raise ValueError(f"CHINESE_BOOK_PARALLEL_PAGES must be between 1 and {POLICY['max_parallel_pages']}")
    return parallel


def process_one(entry, collection, folder, title, keys):
    n=entry['page']; image=collection/entry['image']; raw=collection/'ocr'/image.stem
    try:
        raw.mkdir(parents=True, exist_ok=True, mode=0o700)
        raw_file = raw/f'{image.stem}_res.json'
        if not raw_file.exists():
            response_path = raw/'vision-response.json'
            if response_path.exists(): response = read(response_path)
            else:
                response = call_vision(image, keys['vision_api_key'])
                write_json(response_path,response)
            write_json(raw_file,convert_response(response))
        page_dir = collection/'pages'/f'{n:03d}'
        previous = page_dir/'annotation'
        if (previous/'response.json').is_file() and (previous/'source.json').is_file():
            # Replay the recorded response first. This can recover after a local
            # validator fix without sending another paid request.
            replay = page_dir/f'annotation-replay-{time.time_ns()}'
            try:
                source = read(previous/'source.json')
                meta = annotate(source,replay,provider='deepseek',model='deepseek-flash',replay=previous/'response.json')
                corrected = read(replay/'result/characters.json')
                annotations = read(replay/'result/annotations.json')
                book = assemble(corrected,annotations,source['image_size'])
                book.update(title=title,source_page=n,printed_page='',view=[0,0,*source['image_size']],
                            image_path=entry['image'],provenance=source.get('provenance',{}))
                atomic(page_dir/'book.json',book)
                record={'page':n,'image':entry['image'],'status':'validated','requests_sent':0,
                        'characters':len(source['characters']),'words':len(book['words']),
                        'corrections':meta['corrections'],'annotation':str(replay.relative_to(collection)),
                        'book':str((page_dir/'book.json').relative_to(collection))}
                atomic(page_dir/'status.json',record)
                return record
            except (ValueError, KeyError, TypeError, RuntimeError, OSError):
                pass
        if page_dir.exists():
            attempts = folder/'previous-attempts'; attempts.mkdir(exist_ok=True, mode=0o700)
            os.rename(page_dir,attempts/f'page-{n:03d}-{time.time_ns()}')
        record=process_page(n,image,raw_file,collection,title,keys['deepseek_api_key'])
        if record['status']=='failed': raise ValueError(record.get('error', 'processing failed'))
        return record
    except Exception as exc:
        raise ValueError(f'Page {n}: {exc}') from exc


def batches(entries, collection, parallel):
    batch=[]; size=0
    for entry in entries:
        # Google receives base64 in JSON; account for expansion and request overhead.
        nbytes=(collection/entry['image']).stat().st_size
        prepared=4*((nbytes+2)//3)+1024
        if batch and (len(batch) >= parallel or size+prepared > MAX_BATCH_BYTES):
            yield batch
            batch=[]; size=0
        batch.append(entry); size+=prepared
    if batch: yield batch


def worker(book_id):
    folder = book_path(book_id); collection = folder/'collection'; meta = read(folder/'book.json')
    manifest = read(collection/'manifest.json'); keys = read(KEYS)
    done = sum(1 for entry in manifest['pages'] if entry.get('book'))
    status = {'state':'running','done':done,'total':len(manifest['pages']),'message':'Starting','pid':os.getpid()}
    atomic(folder/'job.json',status)
    try:
        pending=[]
        for entry in manifest['pages']:
            if entry.get('book'): continue
            page = collection/'pages'/f'{entry["page"]:03d}'
            record = read(page/'status.json', {})
            if record.get('status') in ('validated','image-only') and (page/'book.json').is_file():
                entry.update(record); status['done']+=1
            else: pending.append(entry)
        atomic(collection/'manifest.json',manifest); atomic(folder/'job.json',status)
        for batch in batches(pending, collection, parallel_pages()):
            status['message']=f'Processing {len(batch)} pages in parallel'; atomic(folder/'job.json',status)
            first_error = None
            with ThreadPoolExecutor(max_workers=len(batch)) as pool:
                futures={pool.submit(process_one,entry,collection,folder,meta['title'],keys):entry for entry in batch}
                for future in as_completed(futures):
                    try:
                        record = future.result()
                        futures[future].update(record); status['done']+=1
                        atomic(collection/'manifest.json',manifest); atomic(folder/'job.json',status)
                    except Exception as exc:
                        if first_error is None: first_error = exc
            if first_error: raise first_error
        build(collection,folder/'frontend')
        status.update(state='completed',message='Processing complete')
    except Exception as exc:
        status.update(state='error',message=str(exc) or 'Processing failed')
    atomic(folder/'job.json',status)


if __name__=='__main__':
    if len(sys.argv)==3 and sys.argv[1]=='--worker': worker(sys.argv[2])
